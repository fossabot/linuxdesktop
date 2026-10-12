// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package x11

import (
	"errors"
)

var (
	errInvalidDisplay    = errors.New("invalid display")
	errInvalidScreen     = errors.New("invalid screen")
	errAuthorityProtocol = errors.New("unsupported X11 authority protocol")
)
