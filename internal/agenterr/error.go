package agenterr

import (
	"errors"
	"fmt"
)

type Code string

const (
	InvalidInput     Code = "INVALID_INPUT"
	ModeDenied       Code = "MODE_DENIED"
	RootNotFound     Code = "ROOT_NOT_FOUND"
	PathDenied       Code = "PATH_DENIED"
	NotFound         Code = "NOT_FOUND"
	NotFile          Code = "NOT_FILE"
	NotDirectory     Code = "NOT_DIRECTORY"
	NotText          Code = "NOT_TEXT"
	TooLarge         Code = "TOO_LARGE"
	AlreadyExists    Code = "ALREADY_EXISTS"
	PermissionDenied Code = "PERMISSION_DENIED"
	LaunchFailed     Code = "LAUNCH_FAILED"
	CommandFailed    Code = "COMMAND_FAILED"
	TimedOut         Code = "TIMED_OUT"
	ProcessNotFound  Code = "PROCESS_NOT_FOUND"
	ProcessLimit     Code = "PROCESS_LIMIT"
	InternalError    Code = "INTERNAL_ERROR"
)

type Error struct {
	Code    Code
	Message string
	Err     error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

func New(code Code, message string) error {
	return &Error{Code: code, Message: message}
}

func Wrap(code Code, message string, err error) error {
	return &Error{Code: code, Message: message, Err: err}
}

func CodeOf(err error) Code {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}
