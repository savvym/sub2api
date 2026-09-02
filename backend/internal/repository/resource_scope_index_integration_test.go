//go:build integration

package repository

import (
	"context"
	"testing"
)

func TestResourceScopeIndexesAreValid(t *testing.T) {
	tx := testTx(t)
	assertResourceScopeIndexesValid(t, context.Background(), tx)
}

func TestResourceScopeIndexesAreUsed(t *testing.T) {
	tx := testTx(t)
	assertResourceScopeIndexesAreUsed(t, context.Background(), tx)
}
