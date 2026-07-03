package authorization

import (
	"context"
	"fmt"
	"strings"
)

type Principal struct {
	ID    string
	Roles []string
}

type Operation struct {
	Contour string
	Action  string
	Scope   string
}

type Permission struct {
	Contour string
	Action  string
	Scope   string
}

type Role struct {
	Name        string
	Permissions []Permission
}

type Policy struct {
	Roles map[string]Role
}

type Reason struct {
	Code    string
	Message string
}

type Decision struct {
	Allowed bool
	Reasons []Reason
}

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Authorize(ctx context.Context, policy Policy, principal Principal, operation Operation) (Decision, error) {
	_ = ctx
	_ = s

	operation.Contour = strings.TrimSpace(operation.Contour)
	operation.Action = strings.TrimSpace(operation.Action)
	operation.Scope = strings.TrimSpace(operation.Scope)
	if operation.Contour == "" {
		return Decision{}, fmt.Errorf("контур операции доступа должен быть непустым")
	}
	if operation.Action == "" {
		return Decision{}, fmt.Errorf("действие операции доступа должно быть непустым")
	}

	for _, roleName := range principal.Roles {
		roleName = strings.TrimSpace(roleName)
		role, ok := policy.Roles[roleName]
		if !ok {
			continue
		}
		for _, permission := range role.Permissions {
			if permissionMatches(permission, operation) {
				return Decision{
					Allowed: true,
					Reasons: []Reason{{
						Code:    "permission_matched",
						Message: fmt.Sprintf("Операция разрешена ролью %q.", roleName),
					}},
				}, nil
			}
		}
	}

	return Decision{
		Allowed: false,
		Reasons: []Reason{{
			Code:    "permission_not_found",
			Message: "Политика доступа не содержит разрешения для операции.",
		}},
	}, nil
}

func permissionMatches(permission Permission, operation Operation) bool {
	return valueMatches(permission.Contour, operation.Contour) &&
		valueMatches(permission.Action, operation.Action) &&
		valueMatches(permission.Scope, operation.Scope)
}

func valueMatches(pattern string, value string) bool {
	pattern = strings.TrimSpace(pattern)
	value = strings.TrimSpace(value)
	return pattern == "*" || pattern == value
}
