package api

import "net/http"

func (s *Server) v1ProjectDeployment(w http.ResponseWriter, r *http.Request) {
	app, err := s.Store.GetAppForOwner(r.Context(), r.PathValue("id"), tenantToken(r))
	if err != nil {
		writeV1Err(w, 404, "not_found", "no such app")
		return
	}
	info, err := s.Store.ProjectDeploymentInfo(r.Context(), app.ID)
	if err != nil {
		writeV1Err(w, 503, "deployment_unavailable", "cannot read project deployment")
		return
	}
	writeJSON(w, 200, info)
}
