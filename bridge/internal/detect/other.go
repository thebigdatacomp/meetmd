//go:build !darwin

package detect

import (
	"context"

	"github.com/thebigdatacomp/meetmd/internal/config"
	"github.com/thebigdatacomp/meetmd/internal/session"
)

// Start is a no-op on platforms without a browser detector yet.
func Start(context.Context, *session.Manager, *config.Store) {}
