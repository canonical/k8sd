package types_test

import (
	"testing"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/utils"
	. "github.com/onsi/gomega"
)

func TestDatastoreConfigFromUserFacing(t *testing.T) {
	testCases := []struct {
		name             string
		userFacingConfig apiv2.UserFacingDatastoreConfig
		expectedConfig   types.Datastore
		expectedError    bool
	}{
		{
			name: "Valid external datastore config",
			userFacingConfig: apiv2.UserFacingDatastoreConfig{
				Type:       utils.Pointer("external"),
				Servers:    utils.Pointer([]string{"server1", "server2"}),
				CACert:     utils.Pointer("ca_cert"),
				ClientCert: utils.Pointer("client_cert"),
				ClientKey:  utils.Pointer("client_key"),
			},
			expectedConfig: types.Datastore{
				Type:               utils.Pointer("external"),
				ExternalServers:    utils.Pointer([]string{"server1", "server2"}),
				ExternalCACert:     utils.Pointer("ca_cert"),
				ExternalClientCert: utils.Pointer("client_cert"),
				ExternalClientKey:  utils.Pointer("client_key"),
			},
		},
		{
			name: "Invalid datastore config type",
			userFacingConfig: apiv2.UserFacingDatastoreConfig{
				Type:       utils.Pointer("etcd"),
				Servers:    utils.Pointer([]string{"server1", "server2"}),
				CACert:     utils.Pointer("ca_cert"),
				ClientCert: utils.Pointer("client_cert"),
				ClientKey:  utils.Pointer("client_key"),
			},
			expectedConfig: types.Datastore{},
			expectedError:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			config, err := types.DatastoreConfigFromUserFacing(tc.userFacingConfig)

			if tc.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(config).To(Equal(tc.expectedConfig))
		})
	}
}

func TestDatastoreToUserFacing(t *testing.T) {
	testCases := []struct {
		name                     string
		datastoreConfig          types.Datastore
		expectedUserFacingConfig apiv2.UserFacingDatastoreConfig
	}{
		{
			name: "Valid datastore to user-facing config",
			datastoreConfig: types.Datastore{
				Type:               utils.Pointer("external"),
				ExternalServers:    utils.Pointer([]string{"server1", "server2"}),
				ExternalCACert:     utils.Pointer("ca_cert"),
				ExternalClientCert: utils.Pointer("client_cert"),
				ExternalClientKey:  utils.Pointer("client_key"),
			},
			expectedUserFacingConfig: apiv2.UserFacingDatastoreConfig{
				Type:       utils.Pointer("external"),
				Servers:    utils.Pointer([]string{"server1", "server2"}),
				CACert:     utils.Pointer("ca_cert"),
				ClientCert: utils.Pointer("client_cert"),
				ClientKey:  utils.Pointer("client_key"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			userFacingConfig := tc.datastoreConfig.ToUserFacing()
			g.Expect(userFacingConfig).To(Equal(tc.expectedUserFacingConfig))
		})
	}
}
