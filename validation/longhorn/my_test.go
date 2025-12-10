//go:build validation

package longhorn

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/stretchr/testify/suite"
)

type MySuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *clusters.ClusterMeta
}

func (l *MySuite) TearDownSuite() {
	l.session.Cleanup()
}

func (l *MySuite) TestSomething() {
	l.T().Log("Bogus test")
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestMySuite(t *testing.T) {
	suite.Run(t, new(MySuite))
}
