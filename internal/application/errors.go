package application

import (
	"errors"

	"stage-rigging-clearance/internal/domain"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func MapError(err error) Error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return Error{"not_found", err.Error(), 404}
	case errors.Is(err, domain.ErrVersionConflict):
		return Error{"version_conflict", err.Error(), 409}
	case errors.Is(err, domain.ErrIdempotency):
		return Error{"idempotency_conflict", err.Error(), 409}
	case errors.Is(err, domain.ErrFrozen):
		return Error{"frozen", err.Error(), 409}
	case errors.Is(err, domain.ErrInvalidState), errors.Is(err, domain.ErrIncomplete):
		return Error{"precondition_failed", err.Error(), 422}
	case errors.Is(err, domain.ErrForbidden):
		return Error{"forbidden", err.Error(), 403}
	case errors.Is(err, domain.ErrInvalidInput):
		return Error{"invalid_input", err.Error(), 400}
	default:
		return Error{"internal_error", "服务暂时无法处理请求", 500}
	}
}
