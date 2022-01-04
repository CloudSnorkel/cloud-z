package providers

type CloudProvider interface {
	Detect() bool
	GetData() ([][]string, error)
}
