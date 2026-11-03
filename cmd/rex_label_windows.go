//go:build windows

package cmd

import "context"

func beginRexTabLabel(context.Context, *rexLabelClient, string) func() {
	return func() {}
}
