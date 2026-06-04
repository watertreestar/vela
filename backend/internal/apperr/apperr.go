package apperr

import (
	"fmt"

	"packaging-platform/backend/internal/domain"
)

type Kind string

const (
	KindValidation Kind = "validation"
	KindNotFound   Kind = "not_found"
	KindConflict   Kind = "conflict"
	KindBusiness   Kind = "business"
	KindResource   Kind = "resource"
	KindDependency Kind = "dependency"
	KindSystem     Kind = "system"
)

type AppError struct {
	Code     string
	Kind     Kind
	Resource string
	Field    string
	Message  string
	Params   map[string]string
	Details  []domain.APIErrorDetail
	Cause    error
}

func New(kind Kind, code string, message string) *AppError {
	return &AppError{Kind: kind, Code: code, Message: message}
}

func Wrap(cause error, kind Kind, code string, message string) *AppError {
	return &AppError{Kind: kind, Code: code, Message: message, Cause: cause}
}

func Validation(code string, message string) *AppError {
	return New(KindValidation, code, message)
}

func NotFound(resource string, id string) *AppError {
	return &AppError{
		Kind:     KindNotFound,
		Code:     "RESOURCE_NOT_FOUND",
		Resource: resource,
		Message:  fmt.Sprintf("%s %s not found", resource, id),
		Params: map[string]string{
			"resource": resource,
			"id":       id,
		},
	}
}

func Conflict(code string, message string) *AppError {
	return New(KindConflict, code, message)
}

func Business(code string, message string) *AppError {
	return New(KindBusiness, code, message)
}

func Dependency(code string, message string, cause error) *AppError {
	return Wrap(cause, KindDependency, code, message)
}

func System(code string, message string, cause error) *AppError {
	return Wrap(cause, KindSystem, code, message)
}

func (err *AppError) WithField(field string) *AppError {
	err.Field = field
	return err
}

func (err *AppError) WithParam(key string, value string) *AppError {
	if err.Params == nil {
		err.Params = map[string]string{}
	}
	err.Params[key] = value
	return err
}

func (err *AppError) WithDetails(details []domain.APIErrorDetail) *AppError {
	err.Details = details
	return err
}

func (err *AppError) Error() string {
	if err == nil {
		return ""
	}
	if err.Message == "" && err.Cause != nil {
		return err.Cause.Error()
	}
	if err.Cause == nil {
		return err.Message
	}
	return err.Message + ": " + err.Cause.Error()
}

func (err *AppError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}
