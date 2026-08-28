package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func TestGetPositionalProjectionNestedFilter(t *testing.T) {
	t.Parallel()

	first := must.NotFail(types.NewDocument("hashedToken", "old"))
	matching := must.NotFail(types.NewDocument("hashedToken", "wanted", "when", "now"))
	arr := must.NotFail(types.NewArray(first, matching))
	filter := must.NotFail(types.NewDocument("services.resume.loginTokens.hashedToken", "wanted"))

	actual, err := getPositionalProjection(arr, filter, "services.resume.loginTokens.$")
	require.NoError(t, err)
	require.Equal(t, 1, actual.Len())
	assert.Equal(t, matching, must.NotFail(actual.Get(0)))
}

func TestGetPositionalProjectionNestedFilterNoMatch(t *testing.T) {
	t.Parallel()

	arr := must.NotFail(types.NewArray(must.NotFail(types.NewDocument("hashedToken", "old"))))
	filter := must.NotFail(types.NewDocument("services.resume.loginTokens.hashedToken", "missing"))

	actual, err := getPositionalProjection(arr, filter, "services.resume.loginTokens.$")
	require.NoError(t, err)
	assert.Equal(t, 0, actual.Len())
}
