package helm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"helm.sh/helm/v3/pkg/action"
	chartpkg "helm.sh/helm/v3/pkg/chart"
	releasepkg "helm.sh/helm/v3/pkg/release"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestIsInvalidPatchError(t *testing.T) {
	statusErr := apierrors.NewInvalid(schema.GroupKind{Group: "", Kind: "Service"}, "coredns", field.ErrorList{
		field.Required(field.NewPath("spec", "ports"), "Required value"),
	})

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "wrapped StatusError Invalid with cannot patch",
			err:      fmt.Errorf("cannot patch %q with kind Service: %w", "coredns", statusErr),
			expected: true,
		},
		{
			name:     "StatusError Invalid without cannot patch (create/admission)",
			err:      statusErr,
			expected: false,
		},
		{
			name:     "create validation failure without cannot patch",
			err:      errors.New(`failed to create resource: Service "coredns" is invalid: spec.ports: Required value`),
			expected: false,
		},
		{
			name:     "stringified Helm patch error",
			err:      errors.New(`cannot patch "coredns" with kind Service: Service "coredns" is invalid: spec.ports: Required value`),
			expected: true,
		},
		{
			name:     "stringified Helm multi-resource patch error",
			err:      errors.New(`cannot patch "coredns" with kind Service: Service "coredns" is invalid: spec.ports: Required value && cannot patch "cm" with kind ConfigMap: ConfigMap "cm" is invalid: data: Required value`),
			expected: true,
		},
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: false,
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: false,
		},
		{
			name:     "RBAC forbidden StatusError",
			err:      apierrors.NewForbidden(schema.GroupResource{Group: "", Resource: "services"}, "coredns", errors.New("access denied")),
			expected: false,
		},
		{
			name:     "stringified Helm forbidden error",
			err:      errors.New(`cannot patch "coredns" with kind Service: services "coredns" is forbidden: User "system:serviceaccount" cannot patch`),
			expected: false,
		},
		{
			name:     "chart template render error",
			err:      errors.New(`template: coredns/templates/service.yaml:12:34: executing "coredns/templates/service.yaml" at <.Values.foo>: nil pointer`),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := isInvalidPatchError(tt.err)
			if actual != tt.expected {
				t.Errorf("isInvalidPatchError(%v) = %v, expected %v", tt.err, actual, tt.expected)
			}
		})
	}
}

func TestUpgradeChart(t *testing.T) {
	cfg := new(action.Configuration)
	chart := &chartpkg.Chart{
		Metadata: &chartpkg.Metadata{
			Name:    "test-chart",
			Version: "0.1.0",
		},
	}

	t.Run("success without retry", func(t *testing.T) {
		calls := 0
		c := &client{
			timeout:    10 * time.Second,
			maxHistory: 5,
			upgradeRunner: func(ctx context.Context, u *action.Upgrade, name string, ch *chartpkg.Chart, vals map[string]any) (*releasepkg.Release, error) {
				calls++
				if u.Force {
					t.Errorf("expected Force to be false on initial attempt")
				}
				return &releasepkg.Release{Name: name}, nil
			},
		}

		err := c.upgradeChart(context.Background(), cfg, "test-chart", "default", chart, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 1 {
			t.Errorf("expected 1 call, got %d", calls)
		}
	})

	t.Run("wrapped StatusError triggers force retry", func(t *testing.T) {
		calls := 0
		statusErr := apierrors.NewInvalid(schema.GroupKind{Group: "", Kind: "Service"}, "coredns", field.ErrorList{
			field.Required(field.NewPath("spec", "ports"), "Required value"),
		})

		c := &client{
			timeout:    10 * time.Second,
			maxHistory: 5,
			upgradeRunner: func(ctx context.Context, u *action.Upgrade, name string, ch *chartpkg.Chart, vals map[string]any) (*releasepkg.Release, error) {
				calls++
				if calls == 1 {
					if u.Force {
						t.Errorf("expected Force to be false on first call")
					}
					return nil, fmt.Errorf("cannot patch %q with kind Service: %w", "coredns", statusErr)
				}
				if !u.Force {
					t.Errorf("expected Force to be true on retry call")
				}
				return &releasepkg.Release{Name: name}, nil
			},
		}

		err := c.upgradeChart(context.Background(), cfg, "test-chart", "default", chart, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 2 {
			t.Errorf("expected 2 calls, got %d", calls)
		}
	})

	t.Run("stringified Helm patch error triggers force retry", func(t *testing.T) {
		calls := 0
		helmErr := errors.New(`cannot patch "coredns" with kind Service: Service "coredns" is invalid: spec.ports: Required value`)

		c := &client{
			timeout:    10 * time.Second,
			maxHistory: 5,
			upgradeRunner: func(ctx context.Context, u *action.Upgrade, name string, ch *chartpkg.Chart, vals map[string]any) (*releasepkg.Release, error) {
				calls++
				if calls == 1 {
					if u.Force {
						t.Errorf("expected Force to be false on first call")
					}
					return nil, helmErr
				}
				if !u.Force {
					t.Errorf("expected Force to be true on retry call")
				}
				return &releasepkg.Release{Name: name}, nil
			},
		}

		err := c.upgradeChart(context.Background(), cfg, "test-chart", "default", chart, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 2 {
			t.Errorf("expected 2 calls, got %d", calls)
		}
	})

	nonPatchErrors := []struct {
		name string
		err  error
	}{
		{
			name: "context deadline exceeded",
			err:  context.DeadlineExceeded,
		},
		{
			name: "context canceled",
			err:  context.Canceled,
		},
		{
			name: "RBAC forbidden StatusError",
			err:  apierrors.NewForbidden(schema.GroupResource{Group: "", Resource: "services"}, "coredns", errors.New("access denied")),
		},
		{
			name: "stringified Helm forbidden error",
			err:  errors.New(`cannot patch "coredns" with kind Service: services "coredns" is forbidden: User "system:serviceaccount" cannot patch`),
		},
		{
			name: "chart template render error",
			err:  errors.New(`template: coredns/templates/service.yaml:12:34: executing "coredns/templates/service.yaml" at <.Values.foo>: nil pointer`),
		},
	}

	for _, npe := range nonPatchErrors {
		t.Run(fmt.Sprintf("non-patch error does not retry: %s", npe.name), func(t *testing.T) {
			calls := 0
			c := &client{
				timeout:    10 * time.Second,
				maxHistory: 5,
				upgradeRunner: func(ctx context.Context, u *action.Upgrade, name string, ch *chartpkg.Chart, vals map[string]any) (*releasepkg.Release, error) {
					calls++
					if u.Force {
						t.Errorf("expected Force to be false")
					}
					return nil, npe.err
				},
			}

			err := c.upgradeChart(context.Background(), cfg, "test-chart", "default", chart, nil)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, npe.err) && !strings.Contains(err.Error(), npe.err.Error()) {
				t.Errorf("expected error to wrap %v, got %v", npe.err, err)
			}
			if calls != 1 {
				t.Errorf("expected exactly 1 call (no retry), got %d", calls)
			}
		})
	}

	t.Run("force retry also fails surfaces both errors", func(t *testing.T) {
		calls := 0
		initialErr := errors.New(`cannot patch "coredns" with kind Service: Service "coredns" is invalid: spec.ports: Required value`)
		forceErr := errors.New("cannot replace resource: connection reset by peer")

		c := &client{
			timeout:    10 * time.Second,
			maxHistory: 5,
			upgradeRunner: func(ctx context.Context, u *action.Upgrade, name string, ch *chartpkg.Chart, vals map[string]any) (*releasepkg.Release, error) {
				calls++
				if calls == 1 {
					return nil, initialErr
				}
				return nil, forceErr
			},
		}

		err := c.upgradeChart(context.Background(), cfg, "test-chart", "default", chart, nil)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if calls != 2 {
			t.Errorf("expected 2 calls, got %d", calls)
		}
		errMsg := err.Error()
		if !strings.Contains(errMsg, "force retry also failed") {
			t.Errorf("expected error message to mention 'force retry also failed', got: %s", errMsg)
		}
		if !strings.Contains(errMsg, initialErr.Error()) {
			t.Errorf("expected error message to contain initial error %q, got: %s", initialErr.Error(), errMsg)
		}
		if !strings.Contains(errMsg, forceErr.Error()) {
			t.Errorf("expected error message to contain force retry error %q, got: %s", forceErr.Error(), errMsg)
		}
	})
}
