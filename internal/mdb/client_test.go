package mdb

import (
	"testing"

	"chloe/internal/model"
)

// An SRV host is pasted into a URI, so anything but a DNS name is refused
// rather than read as URI options or credentials.
func TestSRVHost(t *testing.T) {
	for _, h := range []string{"cluster0.ab1cd.mongodb.net", "localhost"} {
		if _, err := Options(model.Connection{Host: h, Mongo: model.MongoConfig{SRV: true}}, nil); err != nil {
			t.Errorf("%s: %v", h, err)
		}
	}
	for _, h := range []string{"evil.example/?tls=false", "u:p@evil.example", "a.example,b.example", "", "-x.example", "a.example:27017"} {
		if _, err := Options(model.Connection{Host: h, Mongo: model.MongoConfig{SRV: true}}, nil); err == nil {
			t.Errorf("%q accepted", h)
		}
	}
}
