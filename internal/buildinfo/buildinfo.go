// Package buildinfo holds build metadata.
// These dev defaults are overwritten by CI workflows (ci.yml, publish-images.yml)
// or the consumer's deploy workflow before building (see README → CI pipeline).
package buildinfo

const (
	Commit    = "dev"
	BuildTime = "now"
	Source    = "local"
)
