// Copyright 2021 FerretDB Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metadata

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"slices"

	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// Settings represents collection settings.
type Settings struct {
	UUID            string      `json:"uuid"`
	Indexes         []IndexInfo `json:"indexes"`
	CappedSize      int64       `json:"cappedSize"`
	CappedDocuments int64       `json:"cappedDocuments"`
}

// IndexInfo represents information about a single index.
type IndexInfo struct {
	Name string         `json:"name"`
	Key  []IndexKeyPair `json:"key"`
	// ExpireAfterSeconds, when non-nil, marks this as a TTL index.
	ExpireAfterSeconds *int32 `json:"expireAfterSeconds,omitempty"`
	Unique             bool   `json:"unique"`
	// TextOptions, when non-nil, marks this as a text index and carries its options.
	TextOptions *TextIndexOptions `json:"textOptions,omitempty"`
	// Hidden, when true, marks the index as hidden. Stored and reported but not enforced.
	Hidden bool `json:"hidden,omitempty"`
	// Collation, when set, holds the sjson-marshaled collation document. Stored and reported
	// but not enforced.
	Collation json.RawMessage `json:"collation,omitempty"`
	// PartialFilterExpression, when set, holds the sjson-marshaled partial filter document.
	// Stored and reported but not enforced.
	PartialFilterExpression json.RawMessage `json:"partialFilterExpression,omitempty"`
	// Sphere2DIndexVersion, when non-zero, is the 2dsphereIndexVersion option of a 2dsphere index.
	Sphere2DIndexVersion int32 `json:"sphere2dIndexVersion,omitempty"`
}

// TextIndexOptions holds the options of a MongoDB text index. Note that FerretDB
// stores these options so that the index round-trips through listIndexes, but it
// does not build a real inverted (full-text) index; see the $text query operator.
type TextIndexOptions struct {
	Weights          map[string]int32 `json:"weights,omitempty"`
	DefaultLanguage  string           `json:"defaultLanguage,omitempty"`
	LanguageOverride string           `json:"languageOverride,omitempty"`
	TextIndexVersion int32            `json:"textIndexVersion,omitempty"`
}

// IndexKeyPair consists of a field name and a sort order that are part of the index.
type IndexKeyPair struct {
	Field      string `json:"field"`
	Descending bool   `json:"descending"`
	// Text marks this key as a text index field (its requested value was "text").
	Text bool `json:"text,omitempty"`
	// Sphere2D marks this key as a 2dsphere index field (its requested value was "2dsphere").
	Sphere2D bool `json:"sphere2d,omitempty"`
}

// deepCopy returns a deep copy.
func (s Settings) deepCopy() Settings {
	indexes := make([]IndexInfo, len(s.Indexes))

	for i, index := range s.Indexes {
		indexes[i] = IndexInfo{
			Name:                 index.Name,
			Key:                  slices.Clone(index.Key),
			ExpireAfterSeconds:   index.ExpireAfterSeconds,
			Unique:               index.Unique,
			Hidden:               index.Hidden,
			Sphere2DIndexVersion: index.Sphere2DIndexVersion,
		}

		if index.Collation != nil {
			indexes[i].Collation = slices.Clone(index.Collation)
		}

		if index.PartialFilterExpression != nil {
			indexes[i].PartialFilterExpression = slices.Clone(index.PartialFilterExpression)
		}

		if index.TextOptions != nil {
			opts := *index.TextOptions

			if index.TextOptions.Weights != nil {
				opts.Weights = make(map[string]int32, len(index.TextOptions.Weights))
				for k, v := range index.TextOptions.Weights {
					opts.Weights[k] = v
				}
			}

			indexes[i].TextOptions = &opts
		}
	}

	return Settings{
		UUID:            s.UUID,
		Indexes:         indexes,
		CappedSize:      s.CappedSize,
		CappedDocuments: s.CappedDocuments,
	}
}

// Value implements driver.Valuer interface.
func (s Settings) Value() (driver.Value, error) {
	res, err := json.Marshal(s)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	return string(res), nil
}

// Scan implements sql.Scanner interface.
func (s *Settings) Scan(src any) error {
	var err error

	switch src := src.(type) {
	case nil:
		*s = Settings{}
	case []byte:
		err = json.Unmarshal(src, s)
	case string:
		err = json.Unmarshal([]byte(src), s)
	default:
		panic("can't scan collection settings")
	}

	if err != nil {
		return lazyerrors.Error(err)
	}

	return nil
}

// check interfaces
var (
	_ driver.Valuer = Settings{}
	_ sql.Scanner   = (*Settings)(nil)
)
