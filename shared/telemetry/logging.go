package telemetry

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

type LogLevel string

const (
	LogLevelDebug LogLevel = "DEBUG"
	LogLevelInfo  LogLevel = "INFO"
	LogLevelWarn  LogLevel = "WARN"
	LogLevelError LogLevel = "ERROR"
	LogLevelFatal LogLevel = "FATAL"
)

type LogEntry struct {
	Timestamp     string            `json:"timestamp"`
	Level         string            `json:"level"`
	Message       string            `json:"message"`
	Service       string            `json:"service,omitempty"`
	RequestID     string            `json:"request_id,omitempty"`
	TraceID       string            `json:"trace_id,omitempty"`
	TransactionID string            `json:"transaction_id,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	Error         string            `json:"error,omitempty"`
}

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)password`),
	regexp.MustCompile(`(?i)token`),
	regexp.MustCompile(`(?i)secret`),
	regexp.MustCompile(`(?i)authorization`),
	regexp.MustCompile(`(?i)credit_card`),
	regexp.MustCompile(`(?i)cvv`),
	regexp.MustCompile(`(?i)ssn`),
	regexp.MustCompile(`(?i)social_security`),
}

type StructuredLogger struct {
	service string
	level   LogLevel
	output  io.Writer
	fields  map[string]string
}

func NewStructuredLogger(service string, level LogLevel) *StructuredLogger {
	return &StructuredLogger{
		service: service,
		level:   level,
		output:  os.Stdout,
		fields:  make(map[string]string),
	}
}

func NewStructuredLoggerWithOutput(service string, level LogLevel, output io.Writer) *StructuredLogger {
	return &StructuredLogger{
		service: service,
		level:   level,
		output:  output,
		fields:  make(map[string]string),
	}
}

func (l *StructuredLogger) SetField(key, value string) {
	l.fields[key] = value
}

func (l *StructuredLogger) SetRequestID(requestID string) {
	l.fields["request_id"] = requestID
}

func (l *StructuredLogger) SetTraceID(traceID string) {
	l.fields["trace_id"] = traceID
}

func (l *StructuredLogger) SetTransactionID(transactionID string) {
	l.fields["transaction_id"] = transactionID
}

func (l *StructuredLogger) Debug(msg string, attrs map[string]string) {
	l.log(LogLevelDebug, msg, attrs)
}

func (l *StructuredLogger) Info(msg string, attrs map[string]string) {
	l.log(LogLevelInfo, msg, attrs)
}

func (l *StructuredLogger) Warn(msg string, attrs map[string]string) {
	l.log(LogLevelWarn, msg, attrs)
}

func (l *StructuredLogger) Error(msg string, attrs map[string]string) {
	l.log(LogLevelError, msg, attrs)
}

func (l *StructuredLogger) Fatal(msg string, attrs map[string]string) {
	l.log(LogLevelFatal, msg, attrs)
	os.Exit(1)
}

func (l *StructuredLogger) log(level LogLevel, msg string, attrs map[string]string) {
	if !l.shouldLog(level) {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     string(level),
		Message:   msg,
		Service:   l.service,
	}

	mergedAttrs := make(map[string]string)
	for k, v := range l.fields {
		mergedAttrs[k] = v
	}
	for k, v := range attrs {
		mergedAttrs[k] = v
	}

	entry.Attributes = l.redactSensitiveFields(mergedAttrs)

	if reqID, ok := mergedAttrs["request_id"]; ok {
		entry.RequestID = reqID
		delete(entry.Attributes, "request_id")
	}

	if traceID, ok := mergedAttrs["trace_id"]; ok {
		entry.TraceID = traceID
		delete(entry.Attributes, "trace_id")
	}

	if txID, ok := mergedAttrs["transaction_id"]; ok {
		entry.TransactionID = txID
		delete(entry.Attributes, "transaction_id")
	}

	if errMsg, ok := mergedAttrs["error"]; ok {
		entry.Error = errMsg
		delete(entry.Attributes, "error")
	}

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Fprintf(l.output, `{"timestamp":"%s","level":"ERROR","message":"failed to marshal log entry","error":"%s"}`+"\n", time.Now().UTC().Format(time.RFC3339Nano), err.Error())
		return
	}

	fmt.Fprintln(l.output, string(data))
}

func (l *StructuredLogger) shouldLog(level LogLevel) bool {
	levels := map[LogLevel]int{
		LogLevelDebug: 0,
		LogLevelInfo:  1,
		LogLevelWarn:  2,
		LogLevelError: 3,
		LogLevelFatal: 4,
	}

	return levels[level] >= levels[l.level]
}

func (l *StructuredLogger) redactSensitiveFields(attrs map[string]string) map[string]string {
	result := make(map[string]string)
	for k, v := range attrs {
		if l.isSensitiveField(k) {
			result[k] = l.redactValue(v)
		} else {
			result[k] = v
		}
	}
	return result
}

func (l *StructuredLogger) isSensitiveField(field string) bool {
	for _, pattern := range sensitivePatterns {
		if pattern.MatchString(field) {
			return true
		}
	}
	return false
}

func (l *StructuredLogger) redactValue(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	return value[:2] + strings.Repeat("*", len(value)-4) + value[len(value)-2:]
}

func (l *StructuredLogger) WithFields(fields map[string]string) *StructuredLogger {
	newLogger := &StructuredLogger{
		service: l.service,
		level:   l.level,
		output:  l.output,
		fields:  make(map[string]string),
	}

	for k, v := range l.fields {
		newLogger.fields[k] = v
	}
	for k, v := range fields {
		newLogger.fields[k] = v
	}

	return newLogger
}

func (l *StructuredLogger) Debugf(format string, args ...interface{}) {
	l.Debug(fmt.Sprintf(format, args...), nil)
}

func (l *StructuredLogger) Infof(format string, args ...interface{}) {
	l.Info(fmt.Sprintf(format, args...), nil)
}

func (l *StructuredLogger) Warnf(format string, args ...interface{}) {
	l.Warn(fmt.Sprintf(format, args...), nil)
}

func (l *StructuredLogger) Errorf(format string, args ...interface{}) {
	l.Error(fmt.Sprintf(format, args...), nil)
}
