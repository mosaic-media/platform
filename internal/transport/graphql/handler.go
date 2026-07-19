package graphql

import (
	"encoding/json"
	"net/http"

	"github.com/graphql-go/graphql"
)

// request is the standard GraphQL-over-HTTP POST body.
type request struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName"`
	Variables     map[string]interface{} `json:"variables"`
}

// Handler serves an executable schema as a GraphQL HTTP endpoint. It accepts a
// POST with a JSON body {query, operationName, variables} and returns the
// standard {data, errors} envelope.
//
// It is transport plumbing only: execution, authentication and policy all live
// behind the schema's resolvers, each of which calls an application service
// (the rule boundary_test.go enforces). The handler adds no logic of its own.
func Handler(schema graphql.Schema) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeTransportError(w, http.StatusMethodNotAllowed, "the graphql endpoint accepts POST")
			return
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTransportError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Query == "" {
			writeTransportError(w, http.StatusBadRequest, "query is required")
			return
		}

		result := graphql.Do(graphql.Params{
			Schema:         schema,
			RequestString:  req.Query,
			OperationName:  req.OperationName,
			VariableValues: req.Variables,
			Context:        r.Context(),
		})

		// A GraphQL execution returns HTTP 200 even when fields fail; a
		// resolver's error surfaces in the "errors" array, not the status
		// code. Only a malformed request (above) is a non-200.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

// writeTransportError reports a request the handler rejected before execution,
// in the same {errors:[{message}]} shape a GraphQL error takes so a client
// parses one envelope either way.
func writeTransportError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]string{{"message": message}},
	})
}
