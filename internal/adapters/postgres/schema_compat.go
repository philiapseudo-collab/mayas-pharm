package postgres

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

func isUndefinedRelationError(err error, relation string) bool {
	if err == nil {
		return false
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code != "42P01" {
			return false
		}
		if relation == "" {
			return true
		}
		return strings.Contains(strings.ToLower(pgErr.Message), strings.ToLower(relation))
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "does not exist") &&
		strings.Contains(lower, strings.ToLower(relation)) &&
		(strings.Contains(lower, "relation") || strings.Contains(lower, "table"))
}

func isUndefinedColumnError(err error, column string) bool {
	if err == nil {
		return false
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code != "42703" {
			return false
		}
		if column == "" {
			return true
		}
		return strings.Contains(strings.ToLower(pgErr.Message), strings.ToLower(column))
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "does not exist") &&
		strings.Contains(lower, strings.ToLower(column)) &&
		strings.Contains(lower, "column")
}

func isOrderHardeningCompatibilityError(err error) bool {
	return isUndefinedColumnError(err, "idempotency_key") ||
		isUndefinedColumnError(err, "expires_at") ||
		isUndefinedColumnError(err, "stock_released")
}

func isPaymentQueueCompatibilityError(err error) bool {
	return isUndefinedRelationError(err, "provider_throttles") ||
		isUndefinedColumnError(err, "expires_at")
}

func isRetryableTransactionError(err error) bool {
	if err == nil {
		return false
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40P01" || pgErr.Code == "40001" || pgErr.Code == "55P03"
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "deadlock detected") ||
		strings.Contains(lower, "serialization failure") ||
		strings.Contains(lower, "could not obtain lock")
}
