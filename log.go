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
	"bufio"
	"context"
	"runtime/trace"
	"sync"
	"sync/atomic"
)

// Level represents a log level.
type Level uint32

// Supported log levels.
const (
	Error Level = iota
	Info
	Debug
)

// reserved built-in keys
const (
	tsKey        = "_ts"
	componentKey = "_component"
	taskKey      = "_task"
	regionKey    = "_region"
	msgKey       = "_msg"
)

var builtinKeys = []string{
	tsKey,
	componentKey,
	taskKey,
	regionKey,
	msgKey,
}

// Logger is a structured, leveled logger which integrates with the Go runtime
// task and region tracing facilities provided by package runtime/trace.
type Logger struct {
	parent  *Logger
	level   *Level // pointer to Level, to avoid alignment issues
	enc     Encoder
	user    Fields
	builtin Fields
}

// New creates a new Logger which encodes logs to enc.
func New(enc Encoder, lv Level) *Logger {
	return &Logger{enc: enc, level: &lv}
}

// NewTask creates a new *trace.Task with the specified context and name,
// and returns a new context for the task, a logger configured to record the
// task name, and the *trace.Task itself, which callers should call End on,
// when the task is complete.
func (l *Logger) NewTask(ctx context.Context, task string) (context.Context, *Logger, *trace.Task) {
	tctx, t := trace.NewTask(ctx, task)
	return tctx, l.derive().setBuiltin(Fields{taskKey: task}), t
}

// StartRegion starts a new *trace.Region with the specified context and
// name. It returns a logger configured to record the region name, and the
// associated *trace.Region, which callers should call End on, when exiting
// the region. The call to End must happen from the same goroutine which
// called StartRegion.
func (l *Logger) StartRegion(ctx context.Context, region string) (*Logger, *trace.Region) {
	r := trace.StartRegion(ctx, region)
	return l.derive().setBuiltin(Fields{regionKey: region}), r
}

// WithComponent returns a new Logger scoped to a component.
func (l *Logger) WithComponent(component string) *Logger {
	return l.derive().setBuiltin(Fields{componentKey: component})
}

// WithFields returns a new Logger which logs messages with the specified
// fields. The field names "_ts", "_component", "_task", "_region", and "_msg"
// are reserved.
func (l *Logger) WithFields(fields Fields) *Logger {
	return l.derive().setUser(fields)
}

// SetLevel sets the log level to lv.
func (l *Logger) SetLevel(lv Level) {
	atomic.StoreUint32((*uint32)(l.level), uint32(lv))
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

// setUser sets the user fields on a logger. Returns l for convenience
// when chaining.
func (l *Logger) setUser(fields Fields) *Logger {
	l.user = fields
	return l
}

// setBuiltin sets the builtin fields on a logger. Returns l for convenience
// when chaining.
func (l *Logger) setBuiltin(fields Fields) *Logger {
	l.builtin = fields
	return l
}

// An Encoder encodes fields and produces a log message. Implementations of
// Encoder must be safe for concurrent use.
type Encoder interface {
	Encode(Fields)
}

type TextEncoder struct {
	mu sync.Mutex
	bw *bufio.Writer
}

// Fields is a collection of fields in a structured log entry.
type Fields map[string]interface{}

func (f Fields) partitionAndSortKeys() (builtin, user []string) {
	return nil, nil
}
