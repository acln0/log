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

package log_test

import (
	"bytes"
	"strings"
	"testing"

	"acln.ro/log"
)

func TestLevels(t *testing.T) {
	buf := new(bytes.Buffer)
	enc := &log.TextSink{Output: buf}
	logger := log.New(enc, log.Quiet)

	logger.Error("aterror")
	logger.Info("atinfo")
	logger.Debug("atdebug")

	if buf.Len() > 0 {
		t.Fatalf("wrote logs at Quiet level: %q", buf.String())
	}

	logger.SetLevel(log.Error)

	logger.Error("aterror")
	logger.Info("atinfo")
	logger.Debug("atdebug")

	bs := buf.String()
	if !strings.Contains(bs, "aterror") {
		t.Fatalf("didn't write at Error level")
	}
	if strings.Contains(bs, "atinfo") || strings.Contains(bs, "atdebug") {
		t.Fatalf("didn't respect Error level, got %q", bs)
	}

	logger.SetLevel(log.Info)
	logger.Info("atinfo")
	logger.Debug("atdebug")

	bs = buf.String()
	if !strings.Contains(bs, "atinfo") {
		t.Fatalf("didn't write at Info level")
	}
	if strings.Contains(bs, "atdebug") {
		t.Fatalf("didn't respect Info level, got %q", bs)
	}

	logger.SetLevel(log.Debug)
	logger.Debug("atdebug")

	bs = buf.String()
	if !strings.Contains(bs, "atdebug") {
		t.Fatalf("didn't write at Debug level")
	}
}
