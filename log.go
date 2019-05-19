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
	"testing"
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
	enc    Encoder
	kv     KV
}

// New creates a new Logger which encodes logs to enc.
func New(enc Encoder, lv Level) *Logger {
	return &Logger{enc: enc, level: &lv}
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
func (l *Logger) WithKV(kv KV) *Logger {
	return l.derive().setKV(kv)
}

// SetLevel sets the log level to lv.
func (l *Logger) SetLevel(lv Level) {
	atomic.StoreUint32((*uint32)(l.level), uint32(lv))
}

// Error emits a log message at the Error level.
func (l *Logger) Error(args ...interface{}) error {
	if l.loadLevel() >= Error {
		return l.print(Error, args...)
	}
	return nil
}

// Errorf emits a formatted log message at the Error level.
func (l *Logger) Errorf(format string, args ...interface{}) error {
	if l.loadLevel() >= Error {
		return l.printf(Error, format, args...)
	}
	return nil
}

// Info emits a log message at the Info level.
func (l *Logger) Info(args ...interface{}) error {
	if l.loadLevel() >= Info {
		return l.print(Info, args...)
	}
	return nil
}

// Infof emits a formatted log message at the Info level.
func (l *Logger) Infof(format string, args ...interface{}) error {
	if l.loadLevel() >= Info {
		return l.printf(Info, format, args...)
	}
	return nil
}

// Debug emits a log message at the Debug level.
func (l *Logger) Debug(args ...interface{}) error {
	if l.loadLevel() >= Debug {
		return l.print(Debug, args...)
	}
	return nil
}

// Debugf emits a formatted log message at the Debug level.
func (l *Logger) Debugf(format string, args ...interface{}) error {
	if l.loadLevel() >= Debug {
		return l.printf(Debug, format, args...)
	}
	return nil
}

// print emits a log message at the specified level.
func (l *Logger) print(lv Level, args ...interface{}) error {
	if l.kv == nil {
		l.kv = make(KV)
	}
	l.kv[LevelKey] = lv
	l.kv[TSKey] = time.Now().Format(time.RFC3339Nano)
	l.kv[MsgKey] = fmt.Sprint(args...)
	return l.emit()
}

// printf emits a formatted log message at the specified level.
func (l *Logger) printf(lv Level, format string, args ...interface{}) error {
	if l.kv == nil {
		l.kv = make(KV)
	}
	l.kv[LevelKey] = lv
	l.kv[TSKey] = time.Now().Format(time.RFC3339Nano)
	l.kv[MsgKey] = fmt.Sprintf(format, args...)
	return l.emit()
}

// emit merges l.kv with all parent KV's, then emits a log message.
func (l *Logger) emit() error {
	for parent := l.parent; parent != nil; parent = parent.parent {
		l.kv.merge(parent.kv)
	}
	return l.enc.Encode(l.kv)
}

func (l *Logger) loadLevel() Level {
	return Level(atomic.LoadUint32((*uint32)(l.level)))
}

// derive returns a new logger derived from l. The new logger has .parent l, and
// inherits the level and kv's.
func (l *Logger) derive() *Logger {
	return &Logger{
		parent: l,
		level:  l.level,
		enc:    l.enc,
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
func (l *Logger) setKV(kv KV) *Logger {
	l.kv = kv
	return l
}

// An Encoder encodes key-value pairs and produces a log
// message. Implementations of Encoder must be safe for concurrent
// use. Implementations of Encoder which produce output where the order
// of key-value pairs matters should use KV.Keys to determine the order
// prescribed by this package.
type Encoder interface {
	Encode(KV) error
}

// TextEncoder emits textual log messages to an output stream. TextEncoder
// values must not be copied.
type TextEncoder struct {
	mu     sync.Mutex
	Output io.Writer
}

// Encode encodes the specified key-value pairs to text, then writes them out.
// Values are formatted using fmt.Sprint. If the textual representation of
// values contains whitespace or unprintable characters (in accordance with
// unicode.IsPrint), the values are quoted. Encode makes a single Write call
// to the underlying io.Writer.
func (tenc *TextEncoder) Encode(kv KV) error {
	tenc.mu.Lock()
	defer tenc.mu.Unlock()

	_, err := io.WriteString(tenc.Output, kv.String()+"\n")
	return err
}

// JSONEncoder emits JSON objects to an output stream. JSONEncoder values
// must not be copied.
type JSONEncoder struct {
	mu     sync.Mutex
	Output io.Writer
}

// Encode encodes the specified key-value pairs to JSON, then writes them out.
//
// When using JSONEncoder, callers must ensure that all values in the KV
// map can be JSON-encoded, otherwise the resulting object may be malformed,
// or encoding might fail.
//
// Encode makes a single Write call to the underlying io.Writer.
func (jenc *JSONEncoder) Encode(kv KV) error {
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	if err := enc.Encode(kv); err != nil {
		return err
	}

	jenc.mu.Lock()
	defer jenc.mu.Unlock()

	_, err := jenc.Output.Write(buf.Bytes())
	return err
}

// TestLogEncoder emits textual log messages to a testing.TB. TestLogEncoder
// values must not be copied.
type TestLogEncoder struct {
	mu sync.Mutex
	TB testing.TB
}

// Encode encodes the specified key-value pairs to text, then writes them
// to the associated testing.TB's log. The encoding is identical to that
// of TextEncoder.
func (tle *TestLogEncoder) Encode(kv KV) error {
	tle.mu.Lock()
	defer tle.mu.Unlock()

	tle.TB.Log(kv.String())
	return nil
}

// KV is a collection of key-value pairs.
type KV map[string]interface{}

// String returns a textual representation of the key-value pairs.
func (kv KV) String() string {
	var sb strings.Builder

	keys := kv.Keys()

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

// Keys returns all keys in the map, sorted in the order prescribed by
// this package.  Built-in keys go first, in the order "_level", "_ts",
// "_component", "_task", "_region", "msg", followed by user-defined keys,
// sorted lexicographically.
func (kv KV) Keys() []string {
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
// keys and values in kv.
func (kv KV) merge(other KV) {
	for k, v := range other {
		kv[k] = v
	}
}

// Reserved built-in keys
const (
	LevelKey     = "_level"
	TSKey        = "_ts"
	ComponentKey = "_component"
	TaskKey      = "_task"
	RegionKey    = "_region"
	MsgKey       = "_msg"
)

var builtinKeys = []string{
	LevelKey,
	TSKey,
	ComponentKey,
	TaskKey,
	RegionKey,
	MsgKey,
}

func isBuiltinKey(key string) bool {
	for _, bk := range builtinKeys {
		if bk == key {
			return true
		}
	}
	return false
}
