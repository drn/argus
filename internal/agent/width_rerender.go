package agent

import (
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

type widthRerenderKicker interface {
	KickWidthRerender(*model.Task, config.Config, uint16, uint16) error
}

// KickWidthRerender opts a width-repair caller into bounded stop when its
// provider supports it. Older providers retain the non-escalating behavior.
func KickWidthRerender(r interface {
	KickRerender(*model.Task, config.Config, uint16, uint16) error
}, task *model.Task, cfg config.Config, rows, cols uint16) error {
	if bounded, ok := r.(widthRerenderKicker); ok {
		return bounded.KickWidthRerender(task, cfg, rows, cols)
	}
	return r.KickRerender(task, cfg, rows, cols)
}
