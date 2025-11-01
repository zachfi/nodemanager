/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package freebsd

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	freebsdv1 "github.com/zachfi/nodemanager/api/freebsd/v1"
	"github.com/zachfi/nodemanager/pkg/handler"
)

// JailReconciler reconciles a Jail object
type JailReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	tracer trace.Tracer
	logger *slog.Logger
	system handler.System
	cfg    JailConfig
}

func NewJailReconciler(client client.Client, scheme *runtime.Scheme, logger *slog.Logger, cfg JailConfig, system handler.System) *JailReconciler {
	return &JailReconciler{
		Client: client,
		Scheme: scheme,
		tracer: otel.Tracer("controller.common.configset"),
		logger: logger.With("controller", "configset"),
		system: system,
		cfg:    cfg,
	}
}

// +kubebuilder:rbac:groups=freebsd.nodemanager,resources=jails,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=freebsd.nodemanager,resources=jails/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=freebsd.nodemanager,resources=jails/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Jail object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.1/pkg/reconcile
func (r *JailReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	// TODO(user): your logic here

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *JailReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&freebsdv1.Jail{}).
		Named("freebsd-jail").
		Complete(r)
}
