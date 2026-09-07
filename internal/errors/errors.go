package errors

import (
	"fmt"
	"time"
)

type AppError struct {
	Status int

	Code Code

	Category Category

	Message string

	Details interface{}

	Retryable bool

	RequestID string

	Timestamp time.Time

	cause error
}

func (e *AppError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *AppError) Cause() error {
	return e.Unwrap()
}

func (e *AppError) WithCause(cause error) *AppError {
	e.cause = cause
	return e
}

func (e *AppError) WithDetails(details interface{}) *AppError {
	e.Details = details
	return e
}

func (e *AppError) WithRequestID(id string) *AppError {
	e.RequestID = id
	return e
}

func (e *AppError) WithRetryable(retryable bool) *AppError {
	e.Retryable = retryable
	return e
}

func (e *AppError) WithTimestamp(ts time.Time) *AppError {
	e.Timestamp = ts
	return e
}

type Payload struct {
	Code      string      `json:"code"`
	Category  Category    `json:"category"`
	Message   string      `json:"message"`
	Details   interface{} `json:"details"`
	Retryable bool        `json:"retryable"`
	RequestID string      `json:"request_id"`
	Timestamp string      `json:"timestamp"`
}

func (e *AppError) Payload() *Payload {
	if e == nil {
		return nil
	}
	ts := e.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return &Payload{
		Code:      string(e.Code),
		Category:  e.Category,
		Message:   e.Message,
		Details:   e.Details,
		Retryable: e.Retryable,
		RequestID: e.RequestID,
		Timestamp: ts.UTC().Format(time.RFC3339),
	}
}
