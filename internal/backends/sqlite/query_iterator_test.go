// Copyright 2021 FerretDB Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package sqlite

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestShouldLogQuerySpeed(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		speed *querySpeed
		want  bool
	}{
		"disabled":        {nil, false},
		"small lookup":    {&querySpeed{candidateRows: 2, queryDuration: time.Millisecond, decodeDuration: time.Millisecond}, false},
		"many candidates": {&querySpeed{candidateRows: 100}, true},
		"slow query":      {&querySpeed{queryDuration: 10 * time.Millisecond}, true},
		"slow decode":     {&querySpeed{decodeDuration: 10 * time.Millisecond}, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, shouldLogQuerySpeed(tc.speed))
		})
	}
}
