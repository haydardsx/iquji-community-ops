package application

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func stableErrorCode(err error, fallback string) string {
	if !domain.IsPublicDiagnosticCode(fallback) {
		fallback = "OPERATION_FAILED"
	}
	if err == nil {
		return fallback
	}
	if errors.Is(err, context.Canceled) {
		return "OPERATION_CANCELLED"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "OPERATION_TIMEOUT"
	}
	for _, token := range strings.FieldsFunc(err.Error(), func(value rune) bool {
		return value != '_' && !unicode.IsLetter(value) && !unicode.IsNumber(value)
	}) {
		if domain.IsPublicDiagnosticCode(token) {
			return token
		}
	}
	return fallback
}
