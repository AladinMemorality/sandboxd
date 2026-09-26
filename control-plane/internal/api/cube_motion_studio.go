package api

import (
	"context"
	"net/http"

	"github.com/tastyeffectco/sandboxd/control-plane/internal/egress"
)

func (s *Server) cubeNamedServices() map[string]http.Handler {
	services := map[string]http.Handler{"model": s.cubeEgressModelHandler(), "bridge": http.HandlerFunc(s.cubeEgressBridge)}
	if s.cubeEgress != nil && s.cubeEgress.motion != nil {
		services["motion"] = s.cubeEgress.motion
	}
	return services
}

func (s *Server) authorizeCubeMotionStudio(ctx context.Context, id egress.Identity, appID string) bool {
	if ctx.Err() != nil || s.cubeEgress == nil || s.cubeEgress.config.MotionStudioAppID != appID || !s.cubeEgressIdentityActive(ctx, id) {
		return false
	}
	row, err := s.Store.Get(ctx, id.SandboxID)
	if err != nil || !row.AppID.Valid || row.AppID.String != appID || row.RuntimeProvider != "cube" || row.Status != "running" {
		return false
	}
	current, err := s.Store.CurrentSandboxForApp(ctx, appID)
	return err == nil && current.ID == id.SandboxID
}
