// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package graphql

import (
	"encoding/json"

	"github.com/graphql-go/graphql"

	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
)

// The content projection surface. Every resolver here calls exactly one
// app.Service content method, passing a v1.Caller built from the request's
// session argument (ADR 0017) — the same opaque reference a compiled-in
// capability forwards. Content models come from the published SDK
// (contracts/platform/v1); this package maps them to GraphQL and nothing more.

// caller builds the v1.Caller a content command or query carries. An explicit
// callerSessionId argument wins; absent one, the session an Authorization:
// Bearer header supplied (ADR 0029) is used — so an action a client dispatches,
// which carries the session as a header rather than an argument, still resolves
// a caller.
func caller(p graphql.ResolveParams) v1.Caller {
	if sid := argString(p, "callerSessionId"); sid != "" {
		return v1.CallerFromSession(sid)
	}
	return v1.CallerFromSession(sessionFromContext(p.Context))
}

func argString(p graphql.ResolveParams, name string) string {
	if v, ok := p.Args[name].(string); ok {
		return v
	}
	return ""
}

func argFloat(p graphql.ResolveParams, name string) float64 {
	if v, ok := p.Args[name].(float64); ok {
		return v
	}
	return 0
}

func argInt(p graphql.ResolveParams, name string) int {
	if v, ok := p.Args[name].(int); ok {
		return v
	}
	return 0
}

func argBool(p graphql.ResolveParams, name string) bool {
	v, _ := p.Args[name].(bool)
	return v
}

// optionalBytes maps an absent-or-empty JSON string argument to nil, so the
// service applies its empty-document default rather than storing "".
func optionalBytes(s string) []byte {
	if s == "" {
		return nil
	}
	return []byte(s)
}

// sourceField projects one field of a value type T carried as the resolver
// source — the compact form the virtual-content types use, where every field is
// a plain projection of the source struct.
func sourceField[T any](typ graphql.Output, get func(T) interface{}) *graphql.Field {
	return &graphql.Field{Type: typ, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
		return get(p.Source.(T)), nil
	}}
}

// nodeType projects v1.Node. Every field resolves explicitly because the SDK
// uses named string types and raw JSON []byte, which graphql-go's reflection
// default does not serialize.
var nodeType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Node",
	Fields: graphql.Fields{
		"id":            strField(func(n v1.Node) string { return string(n.ID) }),
		"workId":        strField(func(n v1.Node) string { return string(n.WorkID) }),
		"parentId":      strField(func(n v1.Node) string { return nodeIDPtr(n.ParentID) }),
		"kind":          strField(func(n v1.Node) string { return string(n.Kind) }),
		"mediaType":     strField(func(n v1.Node) string { return string(n.MediaType) }),
		"containerType": strField(func(n v1.Node) string { return string(n.ContainerType) }),
		"itemType":      strField(func(n v1.Node) string { return string(n.ItemType) }),
		"title":         strField(func(n v1.Node) string { return n.Title }),
		"status":        strField(func(n v1.Node) string { return string(n.Status) }),
		"externalIds":   strField(func(n v1.Node) string { return string(n.ExternalIDs) }),
		"attributes":    strField(func(n v1.Node) string { return string(n.Attributes) }),
		"naturalOrder": &graphql.Field{Type: graphql.Float, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return p.Source.(v1.Node).NaturalOrder, nil
		}},
		"createdAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTime(p.Source.(v1.Node).CreatedAt), nil
		}},
		"updatedAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTime(p.Source.(v1.Node).UpdatedAt), nil
		}},
	},
})

// partType projects v1.Part.
var partType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Part",
	Fields: graphql.Fields{
		"id":               partStr(func(p v1.Part) string { return string(p.ID) }),
		"nodeId":           partStr(func(p v1.Part) string { return string(p.NodeID) }),
		"role":             partStr(func(p v1.Part) string { return string(p.Role) }),
		"editionLabel":     partStr(func(p v1.Part) string { return p.EditionLabel }),
		"locationScheme":   partStr(func(p v1.Part) string { return string(p.Location.Scheme) }),
		"locationProvider": partStr(func(p v1.Part) string { return p.Location.Provider }),
		"locationRef":      partStr(func(p v1.Part) string { return p.Location.Ref }),
		"container":        partStr(func(p v1.Part) string { return p.Container }),
		"videoCodec":       partStr(func(p v1.Part) string { return p.VideoCodec }),
		"audioCodec":       partStr(func(p v1.Part) string { return p.AudioCodec }),
		"hdrFormat":        partStr(func(p v1.Part) string { return p.HDRFormat }),
		"naturalOrder": &graphql.Field{Type: graphql.Float, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return p.Source.(v1.Part).NaturalOrder, nil
		}},
		"width":  &graphql.Field{Type: graphql.Int, Resolve: func(p graphql.ResolveParams) (interface{}, error) { return p.Source.(v1.Part).Width, nil }},
		"height": &graphql.Field{Type: graphql.Int, Resolve: func(p graphql.ResolveParams) (interface{}, error) { return p.Source.(v1.Part).Height, nil }},
		"durationSeconds": &graphql.Field{Type: graphql.Float, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return p.Source.(v1.Part).Duration.Seconds(), nil
		}},
		"bitrateBps": &graphql.Field{Type: graphql.Float, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return float64(p.Source.(v1.Part).BitrateBPS), nil
		}},
		"sizeBytes": &graphql.Field{Type: graphql.Float, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return float64(p.Source.(v1.Part).SizeBytes), nil
		}},
	},
})

// relationType projects v1.Relation.
var relationType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Relation",
	Fields: graphql.Fields{
		"id":         relStr(func(r v1.Relation) string { return string(r.ID) }),
		"fromNodeId": relStr(func(r v1.Relation) string { return string(r.FromNodeID) }),
		"toNodeId":   relStr(func(r v1.Relation) string { return string(r.ToNodeID) }),
		"type":       relStr(func(r v1.Relation) string { return string(r.Type) }),
		"origin":     relStr(func(r v1.Relation) string { return string(r.Origin) }),
		"confidence": &graphql.Field{Type: graphql.Float, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return p.Source.(v1.Relation).Confidence, nil
		}},
		"createdAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTime(p.Source.(v1.Relation).CreatedAt), nil
		}},
	},
})

// sourceBindingType projects v1.SourceBinding.
var sourceBindingType = graphql.NewObject(graphql.ObjectConfig{
	Name: "SourceBinding",
	Fields: graphql.Fields{
		"id":             bindStr(func(b v1.SourceBinding) string { return string(b.ID) }),
		"nodeId":         bindStr(func(b v1.SourceBinding) string { return string(b.NodeID) }),
		"sourceProvider": bindStr(func(b v1.SourceBinding) string { return b.SourceProvider }),
		"sourceRef":      bindStr(func(b v1.SourceBinding) string { return b.SourceRef }),
		"matchMethod":    bindStr(func(b v1.SourceBinding) string { return string(b.MatchMethod) }),
		"status":         bindStr(func(b v1.SourceBinding) string { return string(b.Status) }),
		"matchConfidence": &graphql.Field{Type: graphql.Float, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return p.Source.(v1.SourceBinding).MatchConfidence, nil
		}},
	},
})

// importResultType projects v1.ImportResult — what invoking a capability's
// import did, without re-reading the graph.
var importResultType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ImportResult",
	Fields: graphql.Fields{
		"workId":       &graphql.Field{Type: graphql.String, Resolve: importField(func(r v1.ImportResult) interface{} { return string(r.WorkID) })},
		"alreadyKnown": &graphql.Field{Type: graphql.Boolean, Resolve: importField(func(r v1.ImportResult) interface{} { return r.AlreadyKnown })},
		"containers":   &graphql.Field{Type: graphql.Int, Resolve: importField(func(r v1.ImportResult) interface{} { return r.Containers })},
		"items":        &graphql.Field{Type: graphql.Int, Resolve: importField(func(r v1.ImportResult) interface{} { return r.Items })},
		"parts":        &graphql.Field{Type: graphql.Int, Resolve: importField(func(r v1.ImportResult) interface{} { return r.Parts })},
	},
})

func importField(get func(v1.ImportResult) interface{}) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (interface{}, error) {
		return get(p.Source.(v1.ImportResult)), nil
	}
}

// contentNodePayloadType wraps GetContentNodeResult: a node plus, when asked,
// its direct children.
var contentNodePayloadType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ContentNodePayload",
	Fields: graphql.Fields{
		"node":     &graphql.Field{Type: nodeType},
		"children": &graphql.Field{Type: graphql.NewList(nodeType)},
	},
})

// ---- field-resolver helpers, one per source type ----

func strField(get func(v1.Node) string) *graphql.Field {
	return &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
		return get(p.Source.(v1.Node)), nil
	}}
}
func partStr(get func(v1.Part) string) *graphql.Field {
	return &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
		return get(p.Source.(v1.Part)), nil
	}}
}
func relStr(get func(v1.Relation) string) *graphql.Field {
	return &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
		return get(p.Source.(v1.Relation)), nil
	}}
}
func bindStr(get func(v1.SourceBinding) string) *graphql.Field {
	return &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
		return get(p.Source.(v1.SourceBinding)), nil
	}}
}

func nodeIDPtr(id *v1.NodeID) string {
	if id == nil {
		return ""
	}
	return string(*id)
}

// ---- queries ----

func searchContentField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: graphql.NewList(nodeType),
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"title":           &graphql.ArgumentConfig{Type: graphql.String},
			"mediaType":       &graphql.ArgumentConfig{Type: graphql.String},
			"kind":            &graphql.ArgumentConfig{Type: graphql.String},
			"limit":           &graphql.ArgumentConfig{Type: graphql.Int},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.SearchContent(p.Context, v1.SearchContentQuery{
				Caller:    caller(p),
				Title:     argString(p, "title"),
				MediaType: v1.MediaType(argString(p, "mediaType")),
				Kind:      v1.NodeKind(argString(p, "kind")),
				Limit:     argInt(p, "limit"),
			})
			if err != nil {
				return nil, err
			}
			return result.Nodes, nil
		},
	}
}

func contentNodeField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: contentNodePayloadType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"id":              nonNullString(),
			"withChildren":    &graphql.ArgumentConfig{Type: graphql.Boolean},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.GetContentNode(p.Context, v1.GetContentNodeQuery{
				Caller:       caller(p),
				NodeID:       v1.NodeID(argString(p, "id")),
				WithChildren: argBool(p, "withChildren"),
			})
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"node": result.Node, "children": result.Children}, nil
		},
	}
}

func contentByExternalIDField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: graphql.NewList(nodeType),
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"scheme":          nonNullString(),
			"value":           nonNullString(),
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.FindContentByExternalID(p.Context, v1.FindContentByExternalIDQuery{
				Caller: caller(p),
				Scheme: argString(p, "scheme"),
				Value:  argString(p, "value"),
			})
			if err != nil {
				return nil, err
			}
			return result.Nodes, nil
		},
	}
}

// ---- mutations ----

func addContentWorkField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: nodeType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"mediaType":       nonNullString(),
			"title":           nonNullString(),
			"externalIds":     &graphql.ArgumentConfig{Type: graphql.String},
			"attributes":      &graphql.ArgumentConfig{Type: graphql.String},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.AddContentWork(p.Context, v1.AddContentWorkCommand{
				Caller:      caller(p),
				MediaType:   v1.MediaType(argString(p, "mediaType")),
				Title:       argString(p, "title"),
				ExternalIDs: optionalBytes(argString(p, "externalIds")),
				Attributes:  optionalBytes(argString(p, "attributes")),
			})
			if err != nil {
				return nil, err
			}
			return result.Work, nil
		},
	}
}

func addContentChildField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: nodeType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"parentId":        nonNullString(),
			"kind":            nonNullString(),
			"containerType":   &graphql.ArgumentConfig{Type: graphql.String},
			"itemType":        &graphql.ArgumentConfig{Type: graphql.String},
			"title":           nonNullString(),
			"naturalOrder":    &graphql.ArgumentConfig{Type: graphql.Float},
			"externalIds":     &graphql.ArgumentConfig{Type: graphql.String},
			"attributes":      &graphql.ArgumentConfig{Type: graphql.String},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.AddContentChild(p.Context, v1.AddContentChildCommand{
				Caller:        caller(p),
				ParentID:      v1.NodeID(argString(p, "parentId")),
				Kind:          v1.NodeKind(argString(p, "kind")),
				ContainerType: v1.ContainerType(argString(p, "containerType")),
				ItemType:      v1.ItemType(argString(p, "itemType")),
				Title:         argString(p, "title"),
				NaturalOrder:  argFloat(p, "naturalOrder"),
				ExternalIDs:   optionalBytes(argString(p, "externalIds")),
				Attributes:    optionalBytes(argString(p, "attributes")),
			})
			if err != nil {
				return nil, err
			}
			return result.Node, nil
		},
	}
}

func attachContentPartField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: partType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId":  nonNullString(),
			"nodeId":           nonNullString(),
			"role":             nonNullString(),
			"editionLabel":     &graphql.ArgumentConfig{Type: graphql.String},
			"naturalOrder":     &graphql.ArgumentConfig{Type: graphql.Float},
			"locationScheme":   nonNullString(),
			"locationProvider": &graphql.ArgumentConfig{Type: graphql.String},
			"locationRef":      nonNullString(),
			"container":        &graphql.ArgumentConfig{Type: graphql.String},
			"videoCodec":       &graphql.ArgumentConfig{Type: graphql.String},
			"audioCodec":       &graphql.ArgumentConfig{Type: graphql.String},
			"width":            &graphql.ArgumentConfig{Type: graphql.Int},
			"height":           &graphql.ArgumentConfig{Type: graphql.Int},
			"hdrFormat":        &graphql.ArgumentConfig{Type: graphql.String},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.AttachContentPart(p.Context, v1.AttachContentPartCommand{
				Caller:       caller(p),
				NodeID:       v1.NodeID(argString(p, "nodeId")),
				Role:         v1.PartRole(argString(p, "role")),
				EditionLabel: argString(p, "editionLabel"),
				NaturalOrder: argFloat(p, "naturalOrder"),
				Location: v1.MediaLocation{
					Scheme:   v1.LocationScheme(argString(p, "locationScheme")),
					Provider: argString(p, "locationProvider"),
					Ref:      argString(p, "locationRef"),
				},
				Container:  argString(p, "container"),
				VideoCodec: argString(p, "videoCodec"),
				AudioCodec: argString(p, "audioCodec"),
				Width:      argInt(p, "width"),
				Height:     argInt(p, "height"),
				HDRFormat:  argString(p, "hdrFormat"),
			})
			if err != nil {
				return nil, err
			}
			return result.Part, nil
		},
	}
}

func relateContentField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: relationType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"fromNodeId":      nonNullString(),
			"toNodeId":        nonNullString(),
			"type":            nonNullString(),
			"confidence":      &graphql.ArgumentConfig{Type: graphql.Float},
			"origin":          nonNullString(),
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.RelateContent(p.Context, v1.RelateContentCommand{
				Caller:     caller(p),
				FromNodeID: v1.NodeID(argString(p, "fromNodeId")),
				ToNodeID:   v1.NodeID(argString(p, "toNodeId")),
				Type:       v1.RelationType(argString(p, "type")),
				Confidence: argFloat(p, "confidence"),
				Origin:     v1.RelationOrigin(argString(p, "origin")),
			})
			if err != nil {
				return nil, err
			}
			return result.Relation, nil
		},
	}
}

func bindContentSourceField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: sourceBindingType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"nodeId":          nonNullString(),
			"sourceProvider":  nonNullString(),
			"sourceRef":       nonNullString(),
			"matchConfidence": &graphql.ArgumentConfig{Type: graphql.Float},
			"matchMethod":     nonNullString(),
			"status":          nonNullString(),
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.BindContentSource(p.Context, v1.BindContentSourceCommand{
				Caller:          caller(p),
				NodeID:          v1.NodeID(argString(p, "nodeId")),
				SourceProvider:  argString(p, "sourceProvider"),
				SourceRef:       argString(p, "sourceRef"),
				MatchConfidence: argFloat(p, "matchConfidence"),
				MatchMethod:     v1.MatchMethod(argString(p, "matchMethod")),
				Status:          v1.BindingStatus(argString(p, "status")),
			})
			if err != nil {
				return nil, err
			}
			return result.Binding, nil
		},
	}
}

func resolveContentBindingField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: sourceBindingType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"bindingId":       nonNullString(),
			"resolution":      nonNullString(),
			"moveToNodeId":    &graphql.ArgumentConfig{Type: graphql.String},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.ResolveContentBinding(p.Context, v1.ResolveContentBindingCommand{
				Caller:       caller(p),
				BindingID:    v1.SourceBindingID(argString(p, "bindingId")),
				Resolution:   v1.BindingResolution(argString(p, "resolution")),
				MoveToNodeID: v1.NodeID(argString(p, "moveToNodeId")),
			})
			if err != nil {
				return nil, err
			}
			return result.Binding, nil
		},
	}
}

// contentRefType projects a v1.ContentRef — the handle a virtual result carries
// and a client passes back to materialise it.
var contentRefType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ContentRef",
	Fields: graphql.Fields{
		"provider":       sourceField(graphql.String, func(r v1.ContentRef) interface{} { return r.Provider }),
		"nativeId":       sourceField(graphql.String, func(r v1.ContentRef) interface{} { return r.NativeID }),
		"nativeType":     sourceField(graphql.String, func(r v1.ContentRef) interface{} { return r.NativeType }),
		"mediaType":      sourceField(graphql.String, func(r v1.ContentRef) interface{} { return string(r.MediaType) }),
		"externalScheme": sourceField(graphql.String, func(r v1.ContentRef) interface{} { return r.ExternalScheme }),
		"externalId":     sourceField(graphql.String, func(r v1.ContentRef) interface{} { return r.ExternalID }),
	},
})

// contentRefInputType is the ContentRef a client submits to materialise a
// virtual result. It mirrors contentRefType; the two exist because GraphQL
// keeps input and output object types separate.
var contentRefInputType = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "ContentRefInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"provider":       &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"nativeId":       &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"nativeType":     &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
		"mediaType":      &graphql.InputObjectFieldConfig{Type: graphql.String},
		"externalScheme": &graphql.InputObjectFieldConfig{Type: graphql.String},
		"externalId":     &graphql.InputObjectFieldConfig{Type: graphql.String},
	},
})

// contentRefFromMap reads a ContentRef out of a decoded input map.
func contentRefFromMap(m map[string]interface{}) v1.ContentRef {
	get := func(k string) string { s, _ := m[k].(string); return s }
	return v1.ContentRef{
		Provider: get("provider"), NativeID: get("nativeId"), NativeType: get("nativeType"),
		MediaType: v1.MediaType(get("mediaType")), ExternalScheme: get("externalScheme"), ExternalID: get("externalId"),
	}
}

// importRef reads the content ref from either the SDUI runtime's input:{ref}
// shape (an Invoke action, ADR 0029) or a direct ref argument.
func importRef(p graphql.ResolveParams) v1.ContentRef {
	if input, ok := p.Args["input"].(map[string]interface{}); ok {
		if refMap, ok := input["ref"].(map[string]interface{}); ok {
			return contentRefFromMap(refMap)
		}
	}
	if refMap, ok := p.Args["ref"].(map[string]interface{}); ok {
		return contentRefFromMap(refMap)
	}
	return v1.ContentRef{}
}

// searchResultType projects a v1.SearchResult — a virtual candidate, marked
// whether it is already in the library.
var searchResultType = graphql.NewObject(graphql.ObjectConfig{
	Name: "SearchResult",
	Fields: graphql.Fields{
		"ref":       sourceField(contentRefType, func(r v1.SearchResult) interface{} { return r.Ref }),
		"title":     sourceField(graphql.String, func(r v1.SearchResult) interface{} { return r.Title }),
		"year":      sourceField(graphql.Int, func(r v1.SearchResult) interface{} { return r.Year }),
		"poster":    sourceField(graphql.String, func(r v1.SearchResult) interface{} { return r.Poster }),
		"inLibrary": sourceField(graphql.Boolean, func(r v1.SearchResult) interface{} { return r.InLibrary }),
		"nodeId":    sourceField(graphql.String, func(r v1.SearchResult) interface{} { return string(r.NodeID) }),
	},
})

// moduleCatalogType projects an app.ModuleCatalog — a collection a module
// exposes, tagged with the module that serves it.
var moduleCatalogType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ModuleCatalog",
	Fields: graphql.Fields{
		"moduleId":   sourceField(graphql.String, func(c app.ModuleCatalog) interface{} { return c.ModuleID }),
		"id":         sourceField(graphql.String, func(c app.ModuleCatalog) interface{} { return c.Catalog.ID }),
		"name":       sourceField(graphql.String, func(c app.ModuleCatalog) interface{} { return c.Catalog.Name }),
		"nativeType": sourceField(graphql.String, func(c app.ModuleCatalog) interface{} { return c.Catalog.NativeType }),
	},
})

// catalogItemType projects a v1.CatalogItem — a virtual entry of a collection.
var catalogItemType = graphql.NewObject(graphql.ObjectConfig{
	Name: "CatalogItem",
	Fields: graphql.Fields{
		"ref":       sourceField(contentRefType, func(i v1.CatalogItem) interface{} { return i.Ref }),
		"title":     sourceField(graphql.String, func(i v1.CatalogItem) interface{} { return i.Title }),
		"year":      sourceField(graphql.Int, func(i v1.CatalogItem) interface{} { return i.Year }),
		"poster":    sourceField(graphql.String, func(i v1.CatalogItem) interface{} { return i.Poster }),
		"inLibrary": sourceField(graphql.Boolean, func(i v1.CatalogItem) interface{} { return i.InLibrary }),
		"nodeId":    sourceField(graphql.String, func(i v1.CatalogItem) interface{} { return string(i.NodeID) }),
	},
})

// searchAvailableContentField fans a free-text search out to the enabled
// modules' search providers and returns virtual candidates — discovery with no
// raw id (ADR 0028). It is a query: nothing is written until a result is
// materialised.
func searchAvailableContentField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: graphql.NewList(searchResultType),
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"text":            nonNullString(),
			"mediaType":       &graphql.ArgumentConfig{Type: graphql.String},
			"limit":           &graphql.ArgumentConfig{Type: graphql.Int},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.SearchAvailableContent(p.Context, app.SearchAvailableContentQuery{
				Caller: caller(p), Text: argString(p, "text"),
				MediaType: v1.MediaType(argString(p, "mediaType")), Limit: argInt(p, "limit"),
			})
			if err != nil {
				return nil, err
			}
			return result.Results, nil
		},
	}
}

// moduleCatalogsField lists the collections the enabled modules expose, for the
// admin collection browser.
func moduleCatalogsField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: graphql.NewList(moduleCatalogType),
		Args: graphql.FieldConfigArgument{"callerSessionId": nonNullString()},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.ListModuleCatalogs(p.Context, app.ListModuleCatalogsQuery{Caller: caller(p)})
			if err != nil {
				return nil, err
			}
			return result.Catalogs, nil
		},
	}
}

// catalogItemsField pages one module catalog's virtual items.
func catalogItemsField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: graphql.NewList(catalogItemType),
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"moduleId":        nonNullString(),
			"catalogId":       nonNullString(),
			"nativeType":      &graphql.ArgumentConfig{Type: graphql.String},
			"skip":            &graphql.ArgumentConfig{Type: graphql.Int},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.ListCatalogItems(p.Context, app.ListCatalogItemsQuery{
				Caller: caller(p), ModuleID: argString(p, "moduleId"), CatalogID: argString(p, "catalogId"),
				NativeType: argString(p, "nativeType"), Skip: argInt(p, "skip"),
			})
			if err != nil {
				return nil, err
			}
			return result.Items, nil
		},
	}
}

// importContentField materialises a virtual result into the graph, invoking the
// capability the ref names as its provider and forwarding the caller so the
// module acts as the invoking user (ADR 0017). Unlike the other content
// mutations it maps to a Platform command (app.ImportContentCommand), not a
// published v1 type: a capability is invoked by this, it does not call it.
func importContentField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		// The return type is the JSON scalar, not an object, because the SDUI
		// runtime dispatches an Invoke as `mutation { importContent(input: $input) }`
		// with no sub-selection (ADR 0029) — a field returning an object type
		// would be rejected as needing one. The result's fields are carried in
		// the JSON value instead.
		Type: jsonScalar,
		Args: graphql.FieldConfigArgument{
			// callerSessionId is optional: an Invoke action authenticates by the
			// Authorization header instead (ADR 0029). ref and input are the two
			// ways the ref arrives — a typed argument for a direct caller, or the
			// runtime's input:{ref} envelope for an action.
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.String},
			"ref":             &graphql.ArgumentConfig{Type: contentRefInputType},
			"input":           &graphql.ArgumentConfig{Type: jsonScalar},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.ImportContent(p.Context, app.ImportContentCommand{
				Caller: caller(p),
				Ref:    importRef(p),
			})
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{
				"workId":       string(result.WorkID),
				"alreadyKnown": result.AlreadyKnown,
				"containers":   result.Containers,
				"items":        result.Items,
				"parts":        result.Parts,
			}, nil
		},
	}
}

// moduleSettingsType projects a module's settings — the id and the raw JSON
// document, returned as a string.
var moduleSettingsType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ModuleSettings",
	Fields: graphql.Fields{
		"moduleId": &graphql.Field{Type: graphql.String},
		"settings": &graphql.Field{Type: graphql.String},
	},
})

// configureModuleField sets an optional module's user-managed settings (ADR
// 0021) — for the Stremio module, the addon manifest URLs a user adds. It is
// invoke-compatible (ADR 0029/0038): the return type is the JSON scalar and it
// accepts the runtime's `input: JSON` envelope, so a module's contributed
// settings UI (ADR 0038) can drive it as an Invoke action. moduleId/settings
// also work as typed args for a direct caller.
func configureModuleField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: jsonScalar,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.String},
			"moduleId":        &graphql.ArgumentConfig{Type: graphql.String},
			"settings":        &graphql.ArgumentConfig{Type: graphql.String},
			"input":           &graphql.ArgumentConfig{Type: jsonScalar},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			moduleID, settings := configureArgs(p)
			result, err := svc.ConfigureModule(p.Context, app.ConfigureModuleCommand{
				Caller:   caller(p),
				ModuleID: moduleID,
				Settings: settings,
			})
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"moduleId": result.ModuleID, "settings": string(result.Settings)}, nil
		},
	}
}

// configureArgs reads the module id and settings document from either the
// runtime's input envelope (an Invoke action — settings arrives as a JSON object)
// or the typed arguments (a direct caller — settings arrives as a JSON string).
func configureArgs(p graphql.ResolveParams) (string, []byte) {
	if input, ok := p.Args["input"].(map[string]interface{}); ok {
		moduleID, _ := input["moduleId"].(string)
		var settings []byte
		if s, ok := input["settings"]; ok && s != nil {
			settings, _ = json.Marshal(s)
		}
		return moduleID, settings
	}
	return argString(p, "moduleId"), optionalBytes(argString(p, "settings"))
}

// moduleSettingsUIField resolves a module's own contributed settings screen as a
// serialised UINode (ADR 0038), for the Platform's settings host to render.
func moduleSettingsUIField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: jsonScalar,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.String},
			"moduleId":        nonNullString(),
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.ModuleSettingsUI(p.Context, app.ModuleSettingsUIQuery{
				Caller:   caller(p),
				ModuleID: argString(p, "moduleId"),
			})
			if err != nil {
				return nil, err
			}
			var node interface{}
			if err := json.Unmarshal(result.UI, &node); err != nil {
				return nil, err
			}
			return node, nil
		},
	}
}

// moduleSettingsField reads an optional module's settings.
func moduleSettingsField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: moduleSettingsType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": nonNullString(),
			"moduleId":        nonNullString(),
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.GetModuleSettings(p.Context, app.GetModuleSettingsQuery{
				Caller:   caller(p),
				ModuleID: argString(p, "moduleId"),
			})
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"moduleId": result.ModuleID, "settings": string(result.Settings)}, nil
		},
	}
}

func nonNullString() *graphql.ArgumentConfig {
	return &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}
}
