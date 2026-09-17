package api

import (
	"github.com/tastyeffectco/sandboxd/control-plane/internal/store"
	"net/http"
)

// Collection and body-based routes need the same owner check as ID routes.
func (s *Server) canReadCubeSandbox(r *http.Request, sb *store.Sandbox) bool {
	if !sb.AppID.Valid {
		return false
	}
	_, err := s.Store.GetAppForOwner(r.Context(), sb.AppID.String, tenantToken(r))
	return err == nil
}
