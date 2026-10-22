// Package components supplies explicit, editable image templates. Nothing is
// installed implicitly and templates never run a second Docker lifecycle.
package components

import "github.com/daidaidahaidai/local-mock/internal/catalog/domain"

func Templates() map[string]domain.Component {
	return map[string]domain.Component{
		"nacos": {
			Roles: []string{"registry", "config"}, Image: "nacos/nacos-server:v2.5.1", IPOffset: 10, MemoryMB: 1024,
			Env:       map[string]string{"MODE": "standalone", "PREFER_HOST_MODE": "hostname", "NACOS_AUTH_ENABLE": "false", "JVM_XMS": "256m", "JVM_XMX": "512m", "JVM_XMN": "128m"},
			Endpoints: map[string]int{"http": 8848, "grpc": 9848},
			Readiness: []string{"curl", "-fsS", "http://127.0.0.1:8848/nacos/v1/console/health/readiness"},
		},
		"mysql": {
			Roles: []string{"database"}, Image: "mysql:8.4.11", IPOffset: 30, MemoryMB: 1024,
			Env:       map[string]string{"MYSQL_ALLOW_EMPTY_PASSWORD": "yes", "MYSQL_DATABASE": "localmock", "MYSQL_ROOT_HOST": "%", "MYSQL_INITDB_SKIP_TZINFO": "1"},
			Endpoints: map[string]int{"sql": 3306},
			Readiness: []string{"mysqladmin", "ping", "--host=127.0.0.1", "--user=root", "--silent"},
		},
		"redis": {
			Roles: []string{"cache"}, Image: "redis:7.4.9-alpine", IPOffset: 40, MemoryMB: 256,
			Command:   []string{"redis-server", "--save", "", "--appendonly", "no"},
			Endpoints: map[string]int{"redis": 6379}, Readiness: []string{"redis-cli", "ping"},
		},
	}
}
