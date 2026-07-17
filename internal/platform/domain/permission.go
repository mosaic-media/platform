package domain

// Permission is a capability identifier, for example "media.read".
type Permission string

// Role groups a set of Permissions under a stable name.
type Role struct {
	ID          RoleID
	Name        string
	Permissions []Permission
}

// Grant binds a Role to a User.
type Grant struct {
	UserID UserID
	RoleID RoleID
}

// Attribute is a caller attribute usable by attribute-based policy
// decisions, for example a tenant or device classification.
type Attribute struct {
	Key   string
	Value string
}
