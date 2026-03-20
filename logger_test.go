package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestLoggerBasicLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, LevelDebug, false)

	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	output := buf.String()
	if !strings.Contains(output, "DEBUG: debug message") {
		t.Errorf("expected debug message in output")
	}
	if !strings.Contains(output, "INFO: info message") {
		t.Errorf("expected info message in output")
	}
	if !strings.Contains(output, "WARN: warn message") {
		t.Errorf("expected warn message in output")
	}
	if !strings.Contains(output, "ERROR: error message") {
		t.Errorf("expected error message in output")
	}
}

func TestLoggerLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, LevelWarn, false)

	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	output := buf.String()
	if strings.Contains(output, "DEBUG") {
		t.Errorf("debug message should be filtered out")
	}
	if strings.Contains(output, "INFO") {
		t.Errorf("info message should be filtered out")
	}
	if !strings.Contains(output, "WARN") {
		t.Errorf("warn message should be present")
	}
	if !strings.Contains(output, "ERROR") {
		t.Errorf("error message should be present")
	}
}

func TestLoggerWithFields(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, LevelInfo, false)

	logger.WithField("key", "value").Info("test message")

	output := buf.String()
	if !strings.Contains(output, "key=value") {
		t.Errorf("expected field in output, got: %s", output)
	}
}

func TestLoggerWithMultipleFields(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, LevelInfo, false)

	logger.WithFields(map[string]interface{}{
		"key1": "value1",
		"key2": 42,
	}).Info("test message")

	output := buf.String()
	if !strings.Contains(output, "key1=value1") {
		t.Errorf("expected key1 field in output")
	}
	if !strings.Contains(output, "key2=42") {
		t.Errorf("expected key2 field in output")
	}
}

func TestLoggerJSONMode(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, LevelInfo, true)

	logger.WithField("test_key", "test_value").Info("json test")

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	if entry.Level != "INFO" {
		t.Errorf("expected level INFO, got %s", entry.Level)
	}
	if entry.Message != "json test" {
		t.Errorf("expected message 'json test', got %s", entry.Message)
	}
	if entry.Fields["test_key"] != "test_value" {
		t.Errorf("expected field test_key=test_value")
	}
}

func TestLoggerWithError(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, LevelError, false)

	testErr := &ErrorWithContext{
		Err: errors.New("test error"),
		Op:  "test_operation",
	}

	logger.WithError(testErr).Error("operation failed")

	output := buf.String()
	if !strings.Contains(output, "error=") {
		t.Errorf("expected error field in output")
	}
}

func TestContextLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, LevelInfo, false)

	ctx := ContextWithLogger(context.Background(), logger)
	retrievedLogger := LoggerFromContext(ctx)

	if retrievedLogger != logger {
		t.Errorf("expected to retrieve the same logger from context")
	}
}

func TestWrapError(t *testing.T) {
	originalErr := errors.New("original error")
	wrappedErr := WrapError("test_op", originalErr)

	if wrappedErr == nil {
		t.Fatal("expected non-nil error")
	}

	ewc, ok := wrappedErr.(*ErrorWithContext)
	if !ok {
		t.Fatal("expected ErrorWithContext type")
	}

	if ewc.Op != "test_op" {
		t.Errorf("expected operation 'test_op', got %s", ewc.Op)
	}
	if ewc.Err != originalErr {
		t.Errorf("expected wrapped error to be original error")
	}
}

func TestWrapErrorWithFields(t *testing.T) {
	originalErr := errors.New("original error")
	fields := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
	}
	wrappedErr := WrapErrorWithFields("test_op", originalErr, fields)

	ewc, ok := wrappedErr.(*ErrorWithContext)
	if !ok {
		t.Fatal("expected ErrorWithContext type")
	}

	if ewc.Op != "test_op" {
		t.Errorf("expected operation 'test_op', got %s", ewc.Op)
	}
	if len(ewc.Context) != 2 {
		t.Errorf("expected 2 context fields, got %d", len(ewc.Context))
	}
}

func TestLogErrorWithContext(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, LevelError, false)

	err := WrapErrorWithFields("test_operation", errors.New("test error"), map[string]interface{}{
		"detail": "test detail",
	})

	LogError(logger, err)

	output := buf.String()
	if !strings.Contains(output, "operation=test_operation") {
		t.Errorf("expected operation field in output")
	}
	if !strings.Contains(output, "detail=test detail") {
		t.Errorf("expected detail field in output")
	}
}

func TestGlobalLogger(t *testing.T) {
	// Reset global logger for test
	globalLogger = nil
	once = sync.Once{}

	InitLogger(LevelInfo, false)
	logger := GetLogger()

	if logger == nil {
		t.Fatal("expected non-nil global logger")
	}

	// Test that GetLogger returns the same instance
	logger2 := GetLogger()
	if logger != logger2 {
		t.Error("expected GetLogger to return the same instance")
	}
}
