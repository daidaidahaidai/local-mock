package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/daidaidahaidai/local-mock/internal/execution/application"
	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
)

func (d Driver) Probe(ctx context.Context, r domain.Run, p domain.Probe) (domain.ProbeResult, error) {
	var result domain.ProbeResult
	spec, err := loadSpec(r)
	if err != nil {
		return result, err
	}
	call, err := application.ResolveProbe(spec, p)
	if err != nil {
		return result, err
	}
	if err = d.auditSandbox(ctx, r); err != nil {
		return result, err
	}
	var ids bytes.Buffer
	if err = d.command(ctx, &ids, "ps", "--quiet", "--filter", "label=io.localmock.managed=true", "--filter", "label=io.localmock.run="+r.ID, "--filter", "label=com.docker.compose.service="+runnerName); err != nil {
		return result, err
	}
	list := strings.Fields(ids.String())
	if len(list) != 1 {
		return result, fmt.Errorf("expected one running sandbox probe runner")
	}
	b, err := json.Marshal(call)
	if err != nil {
		return result, err
	}
	var out bytes.Buffer
	err = d.commandInput(ctx, bytes.NewReader(b), &out, "exec", "-i", list[0], "/localmock", "runtime", "probe")
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(out.Bytes(), &result)
	return result, err
}
