package apperror

import "errors"

type AppError struct {
	Kind    string
	Message string
}

func (e *AppError) Error() string { return e.Message }

var (
	ErrNotFound     = &AppError{Kind: "not_found", Message: "资源不存在"}
	ErrUnauthorized = &AppError{Kind: "unauthorized", Message: "请先登录"}
	ErrConflict     = &AppError{Kind: "conflict", Message: "操作冲突，请重试"}
)

func BadRequest(message string) error  { return &AppError{Kind: "validation", Message: message} }
func Unavailable(message string) error { return &AppError{Kind: "unavailable", Message: message} }
func As(err error) (*AppError, bool)   { var app *AppError; ok := errors.As(err, &app); return app, ok }
