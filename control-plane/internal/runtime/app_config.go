package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

type AppConfigRequest struct {
	Env      map[string]string `json:"env"`
	Revision string            `json:"revision"`
}

func (c *Client) ApplyAppConfig(ctx context.Context, req AppConfigRequest) error {
	if err := ValidateAppConfig(req); err != nil {
		return err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	response, err := c.do(ctx, c.http, http.MethodPost, "http://runtimed/config", data)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 202 && response.StatusCode != 204 {
		return &ResponseError{StatusCode: response.StatusCode}
	}
	return nil
}

var appEnvKeyName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,255}$`)

func ValidAppConfigKey(key string) bool {
	upper := strings.ToUpper(key)
	if !appEnvKeyName.MatchString(key) || strings.HasPrefix(upper, "RUNTIMED_") || strings.HasPrefix(upper, "LD_") || strings.HasPrefix(upper, "DYLD_") {
		return false
	}
	switch upper {
	case "HOME", "PATH", "USER", "LOGNAME", "SHELL", "BASH_ENV", "ENV", "NODE_OPTIONS", "PYTHONPATH", "PYTHONHOME":
		return false
	}
	return true
}
func ValidateAppConfig(req AppConfigRequest) error {
	if len(req.Env) > 128 || req.Revision == "" || len(req.Revision) > 256 || strings.ContainsAny(req.Revision, "\x00\r\n") {
		return errors.New("invalid app config")
	}
	for key, value := range req.Env {
		if !ValidAppConfigKey(key) || len(value) > 32768 || strings.ContainsRune(value, 0) {
			return errors.New("invalid app config")
		}
	}
	data, err := json.Marshal(req)
	if err != nil || len(data) > 256<<10 {
		return errors.New("app config exceeds limit")
	}
	return nil
}
