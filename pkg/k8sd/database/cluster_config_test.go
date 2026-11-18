package database_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/canonical/k8sd/pkg/k8sd/database"
	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/k8sd/pkg/utils"
	testenv "github.com/canonical/k8sd/pkg/utils/microcluster"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
	. "github.com/onsi/gomega"
)

func TestClusterConfig(t *testing.T) {
	testenv.WithState(t, func(ctx context.Context, s mctypes.State) {
		t.Run("Set", func(t *testing.T) {
			g := NewWithT(t)
			expectedClusterConfig := types.ClusterConfig{
				Certificates: types.Certificates{
					CACert: utils.Pointer("CA CERT DATA"),
					CAKey:  utils.Pointer("CA KEY DATA"),
				},
			}
			expectedClusterConfig.SetDefaults()

			// Write some config to the database
			err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				_, err := database.SetClusterConfig(context.Background(), tx, expectedClusterConfig)
				g.Expect(err).To(Not(HaveOccurred()))
				return nil
			})
			g.Expect(err).To(Not(HaveOccurred()))

			// Retrieve it and map it to the struct
			err = s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				clusterConfig, err := database.GetClusterConfig(ctx, tx)
				g.Expect(err).To(Not(HaveOccurred()))
				g.Expect(clusterConfig).To(Equal(expectedClusterConfig))
				return nil
			})
			g.Expect(err).To(Not(HaveOccurred()))
		})

		t.Run("CannotUpdateCA", func(t *testing.T) {
			// TODO(neoaggelos): extend this test for all fields that cannot be updated
			g := NewWithT(t)
			expectedClusterConfig := types.ClusterConfig{
				Certificates: types.Certificates{
					CACert: utils.Pointer("CA CERT DATA"),
					CAKey:  utils.Pointer("CA KEY DATA"),
				},
			}
			expectedClusterConfig.SetDefaults()

			err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				_, err := database.SetClusterConfig(context.Background(), tx, types.ClusterConfig{
					Certificates: types.Certificates{
						CACert: utils.Pointer("CA CERT NEW DATA"),
					},
				})
				g.Expect(err).To(HaveOccurred())
				return err
			})
			g.Expect(err).To(HaveOccurred())

			err = s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				clusterConfig, err := database.GetClusterConfig(ctx, tx)
				g.Expect(err).To(Not(HaveOccurred()))
				g.Expect(clusterConfig).To(Equal(expectedClusterConfig))
				return nil
			})
			g.Expect(err).To(Not(HaveOccurred()))
		})

		t.Run("Update", func(t *testing.T) {
			g := NewWithT(t)
			expectedClusterConfig := types.ClusterConfig{
				Certificates: types.Certificates{
					CACert:            utils.Pointer("CA CERT DATA"),
					CAKey:             utils.Pointer("CA KEY DATA"),
					ServiceAccountKey: utils.Pointer("SA KEY DATA"),
				},
				Kubelet: types.Kubelet{
					ClusterDNS: utils.Pointer("10.152.183.10"),
				},
			}
			expectedClusterConfig.SetDefaults()
			// The service IP comes from a request, so it counts as chosen by the user.
			expectedClusterConfig.DNS.ServiceIPPinned = utils.Pointer(true)

			err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				returnedConfig, err := database.SetClusterConfig(context.Background(), tx, types.ClusterConfig{
					Kubelet: types.Kubelet{
						ClusterDNS: utils.Pointer("10.152.183.10"),
					},
					Certificates: types.Certificates{
						ServiceAccountKey: utils.Pointer("SA KEY DATA"),
					},
				})
				g.Expect(returnedConfig).To(Equal(expectedClusterConfig))
				g.Expect(err).To(Not(HaveOccurred()))
				return nil
			})
			g.Expect(err).To(Not(HaveOccurred()))

			err = s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
				clusterConfig, err := database.GetClusterConfig(ctx, tx)
				g.Expect(err).To(Not(HaveOccurred()))
				g.Expect(clusterConfig).To(Equal(expectedClusterConfig))
				return nil
			})
			g.Expect(err).To(Not(HaveOccurred()))
		})

		t.Run("SetClusterDNS", func(t *testing.T) {
			setClusterDNS := func(g Gomega, expected string, ip string) bool {
				var updated bool
				err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
					var err error
					updated, err = database.SetClusterDNS(ctx, tx, expected, ip)
					return err
				})
				g.Expect(err).To(Not(HaveOccurred()))
				return updated
			}
			getClusterConfig := func(g Gomega) types.ClusterConfig {
				var config types.ClusterConfig
				err := s.Database().Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
					var err error
					config, err = database.GetClusterConfig(ctx, tx)
					return err
				})
				g.Expect(err).To(Not(HaveOccurred()))
				return config
			}

			t.Run("StoredIPChanged", func(t *testing.T) {
				g := NewWithT(t)
				before := getClusterConfig(g)

				g.Expect(setClusterDNS(g, "10.152.183.99", "10.152.183.20")).To(BeFalse())
				g.Expect(getClusterConfig(g)).To(Equal(before))
			})

			t.Run("Update", func(t *testing.T) {
				g := NewWithT(t)
				expected := getClusterConfig(g)
				expected.Kubelet.ClusterDNS = utils.Pointer("10.152.183.20")

				// The update is allowed although the IP was pinned, and does not alter the pin.
				g.Expect(setClusterDNS(g, "10.152.183.10", "10.152.183.20")).To(BeTrue())
				g.Expect(getClusterConfig(g)).To(Equal(expected))
			})

			t.Run("AlreadySet", func(t *testing.T) {
				g := NewWithT(t)

				g.Expect(setClusterDNS(g, "10.152.183.99", "10.152.183.20")).To(BeTrue())
			})
		})
	})
}
