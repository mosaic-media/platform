package graphql_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	graphqllib "github.com/graphql-go/graphql"

	graphqltransport "github.com/mosaic-media/mosaic-platform/internal/transport/graphql"
)

// helloSchema is a trivial schema so the handler can be tested as pure
// transport, without a service behind it.
func helloSchema(t *testing.T) graphqllib.Schema {
	t.Helper()
	schema, err := graphqllib.NewSchema(graphqllib.SchemaConfig{
		Query: graphqllib.NewObject(graphqllib.ObjectConfig{
			Name: "Query",
			Fields: graphqllib.Fields{
				"greeting": &graphqllib.Field{
					Type: graphqllib.String,
					Args: graphqllib.FieldConfigArgument{
						"name": &graphqllib.ArgumentConfig{Type: graphqllib.String},
					},
					Resolve: func(p graphqllib.ResolveParams) (interface{}, error) {
						name, _ := p.Args["name"].(string)
						return "hello " + name, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatalf("build schema: %v", err)
	}
	return schema
}

func TestHandlerExecutesAQuery(t *testing.T) {
	srv := httptest.NewServer(graphqltransport.Handler(helloSchema(t)))
	defer srv.Close()

	body := `{"query":"query($n:String){ greeting(name:$n) }","variables":{"n":"mosaic"}}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var out struct {
		Data   map[string]any `json:"data"`
		Errors []any          `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", out.Errors)
	}
	if out.Data["greeting"] != "hello mosaic" {
		t.Fatalf("greeting = %v, want %q", out.Data["greeting"], "hello mosaic")
	}
}

// A resolver error is a 200 with an errors array — the GraphQL-over-HTTP
// convention — not an HTTP error status.
func TestHandlerFieldErrorIs200WithErrors(t *testing.T) {
	srv := httptest.NewServer(graphqltransport.Handler(helloSchema(t)))
	defer srv.Close()

	// Selecting an unknown field is a query error.
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"query":"{ nope }"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 even for a query error", resp.StatusCode)
	}
	var out struct {
		Errors []any `json:"errors"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Errors) == 0 {
		t.Fatal("expected an errors array for an invalid field")
	}
}

func TestHandlerRejectsNonPostAndEmptyQuery(t *testing.T) {
	srv := httptest.NewServer(graphqltransport.Handler(helloSchema(t)))
	defer srv.Close()

	// GET is rejected before execution.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", resp.StatusCode)
	}

	// An empty query is a bad request.
	resp, err = http.Post(srv.URL, "application/json", strings.NewReader(`{"query":""}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty-query status = %d, want 400", resp.StatusCode)
	}
}
