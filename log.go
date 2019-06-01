// Copyright 2019 Andrei Tudor Călin
//
// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
// OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

package log

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// Level represents a log level.
type Level uint32

// String returns a textual representation of the log level. The strings are
// "quiet", "error", "info", and "debug". If lv is not a supported log level,
// String returns the empty string.
func (lv Level) String() string {
	switch lv {
	case Quiet:
		return "quiet"
	case Error:
		return "error"
	case Info:
		return "info"
	case Debug:
		return "debug"
	}
	return ""
}

// MarshalJSON marshals lv as a JSON string.
func (lv Level) MarshalJSON() ([]byte, error) {
	return json.Marshal(lv.String())
}

// Supported log levels.
const (
	Quiet Level = iota
	Error
	Info
	Debug
)

// Logger is a structured, leveled logger.
type Logger struct {
	parent *Logger
	level  *Level // pointer to Level, to avoid alignment issues
	sink   Sink
	kv     KV
}

// New creates a new Logger which forwards logs at or below the specified
// level to the specified Sink.
func New(sink Sink, lv Level) *Logger {
	return &Logger{level: &lv, sink: sink}
}

// ForComponent returns a Logger for the specified component.
func (l *Logger) ForComponent(component string) *Logger {
	return l.derive().set(ComponentKey, component)
}

// ForTask returns a Logger for the specified task. ForTask is intended
// to be used in conjunction with a *runtime/trace.Task. By convention,
// the task names should match.
func (l *Logger) ForTask(task string) *Logger {
	return l.derive().set(TaskKey, task)
}

// ForRegion returns a Logger for the specified region. ForRegion is
// intended to be used in conjunction with a *runtime/trace.Region. By
// convention, the region names should match.
func (l *Logger) ForRegion(region string) *Logger {
	return l.derive().set(RegionKey, region)
}

// WithKV returns a new Logger which logs messages with the specified
// key-value pairs. The keys "_level", "_ts", "_component", "_task",
// "_region", and "_msg" are reserved.
func (l *Logger) WithKV(kver ...KVer) *Logger {
	return l.derive().setKV(kver...)
}

// SetLevel sets the log level to lv.
func (l *Logger) SetLevel(lv Level) {
	atomic.StoreUint32((*uint32)(l.level), uint32(lv))
}

// Error emits a log message at the Error level.
func (l *Logger) Error(err error, kvers ...KVer) error {
	return l.emit(Error, err, kvers...)
}

// Info emits a log message at the Info level.
func (l *Logger) Info(kvers ...KVer) error {
	return l.emit(Info, nil, kvers...)
}

// Debug emits a log message at the Debug level.
func (l *Logger) Debug(kvers ...KVer) error {
	return l.emit(Debug, nil, kvers...)
}

// emit emits a log message at the specified level. It creates a KV with
// level and timestamp keys, adds an error key if err is not nil, merges it
// with the other KVs, as well as all parent KV's, then emits a log message.
func (l *Logger) emit(lv Level, err error, others ...KVer) error {
	if l.loadLevel() < lv {
		return nil
	}
	kv := make(KV)
	kv[LevelKey] = lv
	kv[TimestampKey] = time.Now().Format(time.RFC3339Nano)
	if err != nil {
		kv[ErrorKey] = err.Error()
	}
	for _, other := range others {
		kv.merge(other.KV())
	}
	for parent := l; parent != nil; parent = parent.parent {
		kv.merge(parent.kv)
	}
	return l.sink.Drain(kv)
}

func (l *Logger) loadLevel() Level {
	return Level(atomic.LoadUint32((*uint32)(l.level)))
}

// derive returns a new logger derived from l. The new logger has .parent l, and
// inherits the level and sink.
func (l *Logger) derive() *Logger {
	return &Logger{
		parent: l,
		level:  l.level,
		sink:   l.sink,
	}
}

// set sets a key-value pair. Returns l for convenience when chaining.
func (l *Logger) set(key string, value interface{}) *Logger {
	if l.kv == nil {
		l.kv = make(KV)
	}
	l.kv[key] = value
	return l
}

// setKV replaces l.kv with the specified key-value pairs. Returns l for
// convenience when chaining.
func (l *Logger) setKV(kvers ...KVer) *Logger {
	kv := make(KV)
	for _, kver := range kvers {
		kv.merge(kver.KV())
	}
	l.kv = kv
	return l
}

// A Sink encodes key-value pairs and produces a log message. Implementations
// of Sink must be safe for concurrent use.
//
// Implementations of Sink which produce output where the order of key-value
// pairs is significant should use KV.SortedKeys to determine the order
// prescribed by this package.
//
// Implementations of Sink must not modify KV maps.
type Sink interface {
	Drain(KV) error
}

// TextSink emits textual log messages to an output stream. TextSink values
// must not be copied.
type TextSink struct {
	mu     sync.Mutex
	Output io.Writer
}

// Drain encodes the specified key-value pairs to text, then writes them to
// the underlying io.Writer, followed by a newline.
//
// Values are formatted using fmt.Sprint. If the textual representation of
// values contains whitespace or unprintable characters (in accordance with
// unicode.IsSpace and unicode.IsPrint), the values are quoted.
//
// Drain makes a single Write call to the underlying io.Writer.
func (ts *TextSink) Drain(kv KV) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	_, err := io.WriteString(ts.Output, kv.String()+"\n")
	return err
}

var _ Sink = (*TextSink)(nil)

// JSONSink emits JSON objects to an output stream. JSONSink values must
// not be copied.
type JSONSink struct {
	mu     sync.Mutex
	Output io.Writer
}

// Drain encodes the specified key-value pairs to JSON, then writes them out
// to the underyling io.Writer.
//
// When using JSONSink, callers must ensure that all values in the KV map
// can be JSON-encoded, otherwise the resulting object may be malformed,
// or encoding might fail.
//
// Drain makes a single Write call to the underlying io.Writer.
func (js *JSONSink) Drain(kv KV) error {
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	if err := enc.Encode(kv); err != nil {
		return err
	}

	js.mu.Lock()
	defer js.mu.Unlock()

	_, err := js.Output.Write(buf.Bytes())
	return err
}

var _ Sink = (*JSONSink)(nil)

// Tee is a Sink which sends KVs to all sinks it contains.
type Tee []Sink

// Drain sends kv to all sinks contained in t. If Sink.Drain returns an
// error for any Sink, Drain records the first such error and returns it.
func (t Tee) Drain(kv KV) error {
	var err error

	for _, sink := range t {
		derr := sink.Drain(kv)
		if derr != nil && err == nil {
			err = derr
		}
	}

	return err
}

var _ Sink = (Tee)(nil)

// KVer is any type which can represent itself as a key-value pair.
type KVer interface {
	KV() KV
}

// KV is a collection of key-value pairs.
type KV map[string]interface{}

// String returns a textual representation of the key-value pairs.
func (kv KV) String() string {
	var sb strings.Builder

	keys := kv.SortedKeys()

	for i, key := range keys {
		sb.WriteString(key)
		sb.WriteString("=")

		value := fmt.Sprint(kv[key])
		if shouldQuote(value) {
			value = fmt.Sprintf("%q", value)
		}
		sb.WriteString(value)

		if i < len(keys)-1 {
			sb.WriteString(" ")
		}
	}

	return sb.String()
}

// shouldQuote returns a boolean indicating whether a string in textual log
// output should be quoted.
func shouldQuote(s string) bool {
	idx := strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || !unicode.IsPrint(r)
	})
	return idx != -1
}

// KV returns kv.
func (kv KV) KV() KV {
	return kv
}

// SortedKeys returns all keys in the map, sorted in the order prescribed
// by this package.  Built-in keys go first, in the order "_level", "_ts",
// "_component", "_task", "_region", "_error", followed by user-defined keys,
// sorted lexicographically.
func (kv KV) SortedKeys() []string {
	return append(kv.builtin(), kv.user()...)
}

// builtin returns the builtin keys present in kv, sorted like builtinKeys.
func (kv KV) builtin() []string {
	var bkeys []string
	for _, bk := range builtinKeys {
		if _, ok := kv[bk]; ok {
			bkeys = append(bkeys, bk)
		}
	}
	return bkeys
}

// user returns the user-defined keys present in f, sorted lexicographically.
func (kv KV) user() []string {
	var ukeys []string
	for k := range kv {
		if !isBuiltinKey(k) {
			ukeys = append(ukeys, k)
		}
	}
	sort.Strings(ukeys)
	return ukeys
}

// merge merges kv with another set of key-value pairs, storing the resulting
// keys and values in kv. Keys that exist in kv take precedence over keys that
// exist in the other map.
func (kv KV) merge(other KV) {
	for k, v := range other {
		if _, ok := kv[k]; !ok {
			kv[k] = v
		}
	}
}

// Reserved built-in keys
const (
	LevelKey     = "_level"
	TimestampKey = "_ts"
	ComponentKey = "_component"
	TaskKey      = "_task"
	RegionKey    = "_region"
	ErrorKey     = "_errors"
)

var builtinKeys = []string{
	LevelKey,
	TimestampKey,
	ComponentKey,
	TaskKey,
	RegionKey,
	ErrorKey,
}

func isBuiltinKey(key string) bool {
	for _, bk := range builtinKeys {
		if bk == key {
			return true
		}
	}
	return false
}
