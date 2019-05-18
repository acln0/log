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
)

// Level represents a log level.
type Level uint32

// Supported log levels.
const (
	Quiet Level = iota
	Error
	Info
	Debug
)

// Logger is a structured, leveled logger which integrates with the Go runtime
// task and region tracing facilities provided by package runtime/trace.
type Logger struct {
	parent *Logger
	level  *Level // pointer to Level, to avoid alignment issues
	enc    Encoder
	fields Fields
}

// New creates a new Logger which encodes logs to enc.
func New(enc Encoder, lv Level) *Logger {
	return &Logger{enc: enc, level: &lv}
}

// WithComponent returns a new Logger scoped to a component.
func (l *Logger) WithComponent(component string) *Logger {
	return l.derive().set(componentKey, component)
}

// WithFields returns a new Logger which logs messages with the specified
// fields. The field names "_level", "_ts", "_component", and "_msg"
// are reserved.
func (l *Logger) WithFields(fields Fields) *Logger {
	return l.derive().setFields(fields)
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
		return l.print(Info, args...)
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
	l.fields[levelKey] = lv
	l.fields[tsKey] = time.Now()
	l.fields[msgKey] = fmt.Sprint(args...)
	return l.emit()
}

// printf emits a formatted log message at the specified level.
func (l *Logger) printf(lv Level, format string, args ...interface{}) error {
	l.fields[levelKey] = lv
	l.fields[tsKey] = time.Now()
	l.fields[msgKey] = fmt.Sprintf(format, args...)
	return l.emit()
}

// emit merges l.fields with all parent fields, then emits a log message.
func (l *Logger) emit() error {
	for parent := l.parent; parent != nil; parent = parent.parent {
		l.fields.merge(parent.fields)
	}
	return l.enc.Encode(l.fields)
}

func (l *Logger) loadLevel() Level {
	return Level(atomic.LoadUint32((*uint32)(l.level)))
}

// derive returns a new logger derived from l. The new logger has .parent l, and
// inherits the level and enc fields.
func (l *Logger) derive() *Logger {
	return &Logger{
		parent: l,
		level:  l.level,
		enc:    l.enc,
	}
}

// set sets a field at the specified key to the specified value. Returns l
// for convenience when chaining.
func (l *Logger) set(key string, value interface{}) *Logger {
	if l.fields == nil {
		l.fields = make(Fields)
	}
	l.fields[key] = value
	return l
}

// setFields replaces l.fields with the specified fields map. Returns l
// for convenience when chaining.
func (l *Logger) setFields(fields Fields) *Logger {
	l.fields = fields
	return l
}

// An Encoder encodes fields and produces a log message. Implementations of
// Encoder must be safe for concurrent use. Implementations of Encoder which
// produce output where the order of fields matters should use Fields.Keys
// to determine the order prescribed by this package.
type Encoder interface {
	Encode(Fields) error
}

// TextEncoder emits textual log messages to an output stream.
type TextEncoder struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTextEncoder constructs a new TextEncoder, which writes log lines to w.
func NewTextEncoder(w io.Writer) *TextEncoder {
	return &TextEncoder{w: w}
}

// Encode encodes the specified fields to text, then writes them out.
// Values are formatted using fmt.Sprint. Encode makes a single Write call
// to the underlying io.Writer.
func (tenc *TextEncoder) Encode(fields Fields) error {
	tenc.mu.Lock()
	defer tenc.mu.Unlock()

	_, err := io.WriteString(tenc.w, fields.String())
	return err
}

// JSONEncoder emits JSON objects to an output stream.
type JSONEncoder struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONEncoder constructs a new JSONEncoder, which writes JSON objects to w.
func NewJSONEncoder(w io.Writer) *JSONEncoder {
	return &JSONEncoder{w: w}
}

// Encode encodes the specified fields to JSON, then writes them out.
//
// When using JSONEncoder, callers must ensure that all values in the fields
// map can be JSON-encoded, otherwise the resulting object may be malformed,
// or encoding might fail.
//
// Encode makes a single Write call to the underlying io.Writer.
func (jenc *JSONEncoder) Encode(fields Fields) error {
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	if err := enc.Encode(fields); err != nil {
		return err
	}

	jenc.mu.Lock()
	defer jenc.mu.Unlock()

	_, err := jenc.w.Write(buf.Bytes())
	return err
}

// TestLogEncoder emits textual log messages to a *testing.T.
type TestLogEncoder struct {
	mu sync.Mutex
	t  *testing.T
}

// NewTestLogEncoder constructs a new TestLogEncoder, which writes log lines
// to t's associated logger.
func NewTestLogEncoder(t *testing.T) *TestLogEncoder {
	return &TestLogEncoder{t: t}
}

// Encode encodes the specified fields to text, then writes them to the
// associated *testing.T's log.
func (tle *TestLogEncoder) Encode(fields Fields) error {
	tle.mu.Lock()
	defer tle.mu.Unlock()

	tle.t.Log(fields.String())
	return nil
}

// Fields is a collection of fields in a structured log entry.
type Fields map[string]interface{}

// String returns a textual representation of the fields.
func (f Fields) String() string {
	var sb strings.Builder

	for _, key := range f.Keys() {
		sb.WriteString(key)
		sb.WriteString("=")
		sb.WriteString(fmt.Sprint(f[key]))
	}

	return sb.String()
}

// Keys returns all keys in the map, sorted by the order prescribed by this package.
func (f Fields) Keys() []string {
	return append(f.builtin(), f.user()...)
}

// builtin returns the builtin keys present in f, sorted like builtinKeys.
func (f Fields) builtin() []string {
	var bkeys []string
	for _, bk := range builtinKeys {
		if _, ok := f[bk]; ok {
			bkeys = append(bkeys, bk)
		}
	}
	return bkeys
}

// user returns the user-defined keys present in f, sorted lexicographically.
func (f Fields) user() []string {
	var ukeys []string
	for k := range f {
		if !isBuiltinKey(k) {
			ukeys = append(ukeys, k)
		}
	}
	sort.Strings(ukeys)
	return ukeys
}

// merge merges f with another set of fields, and stores results in f.
func (f Fields) merge(other Fields) {
	for k, v := range other {
		f[k] = v
	}
}

// reserved built-in keys
const (
	levelKey     = "_level"
	tsKey        = "_ts"
	componentKey = "_component"
	msgKey       = "_msg"
)

var builtinKeys = []string{
	levelKey,
	tsKey,
	componentKey,
	msgKey,
}

func isBuiltinKey(key string) bool {
	for _, bk := range builtinKeys {
		if bk == key {
			return true
		}
	}
	return false
}
