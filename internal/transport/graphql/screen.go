// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package graphql

import (
	"encoding/json"

	"github.com/graphql-go/graphql"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/transport/screens"
)

// screenField serves a server-emitted SDUI screen (ADR 0029). It returns the
// UINode tree as a JSON string — the same string-encoded-JSON convention the
// module settings field uses — which the Shell parses and renders. params is a
// JSON object string; the caller is forwarded so the screen shows only what its
// caller may see.
func screenField(svc *screens.Service) *graphql.Field {
	return &graphql.Field{
		Type: graphql.String,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"name":            nonNullString(),
			"params":          &graphql.ArgumentConfig{Type: graphql.String},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var params map[string]any
			if raw := argString(p, "params"); raw != "" {
				if err := json.Unmarshal([]byte(raw), &params); err != nil {
					return nil, contracts.NewError(contracts.InvalidArgument, "params must be a JSON object")
				}
			}
			node, err := svc.Render(p.Context, argString(p, "name"), caller(p), params)
			if err != nil {
				return nil, err
			}
			b, err := json.Marshal(node)
			if err != nil {
				return nil, contracts.WrapError(contracts.Internal, "encode screen", err)
			}
			return string(b), nil
		},
	}
}
