package providers

import "cloud-z/reporting"

type CloudProvider interface {
	Detect() bool
	GetData(*reporting.Report)
}
