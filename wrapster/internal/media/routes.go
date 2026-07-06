package media

import "strings"

type serviceRoute struct {
	Service  string
	Action   string
	StreamID string
}

func parseServiceRoute(requestPath, prefix string) (serviceRoute, bool) {
	if !strings.HasPrefix(requestPath, prefix) {
		return serviceRoute{}, false
	}
	rest := strings.TrimPrefix(requestPath, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return serviceRoute{}, false
	}
	service, action := parts[0], parts[1]
	if !validService(service) {
		return serviceRoute{}, false
	}
	switch action {
	case "random-song", "search":
		if len(parts) != 2 {
			return serviceRoute{}, false
		}
		return serviceRoute{Service: service, Action: action}, true
	case "stream":
		if len(parts) != 3 || parts[2] == "" {
			return serviceRoute{}, false
		}
		return serviceRoute{Service: service, Action: action, StreamID: parts[2]}, true
	default:
		return serviceRoute{}, false
	}
}

func validService(service string) bool {
	return service == "jellyfin" || service == "plex"
}
