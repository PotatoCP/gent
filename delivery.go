package gent

import (
	core "github.com/rickchristie/gent/internal/core"
)

// DeliveryConfig bounds one live consumer independently of checkpoint persistence.
// Zero fields use defaults. Negative fields are invalid; unbounded delivery is not supported.
type DeliveryConfig = core.DeliveryConfig

// ErrDeliveryOverflow means a consumer exceeded its event or byte queue limit.
var ErrDeliveryOverflow = core.ErrDeliveryOverflow

// ErrDeliveryTimeout means a consumer did not receive an event within its delivery timeout.
var ErrDeliveryTimeout = core.ErrDeliveryTimeout
