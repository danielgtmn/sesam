package memadapter_test

import (
	"testing"

	"github.com/danielgtmn/sesam/adapter"
	"github.com/danielgtmn/sesam/adapters/adaptertest"
	"github.com/danielgtmn/sesam/adapters/memadapter"
)

func TestConformance(t *testing.T) {
	adaptertest.Run(t, func(t *testing.T) adapter.Adapter { return memadapter.New() })
}
