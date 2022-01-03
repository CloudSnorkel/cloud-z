package providers

type Provider interface {
	Detect() bool
	GetData() ([][]string, error)
}
