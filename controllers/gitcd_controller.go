/*
Copyright 2021.

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

package controllers

import (
	"context"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/memory"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"time"

	gitv1alpha1 "github.com/jiejhih/cd-operator/api/v1alpha1"
)

// GitCDReconciler reconciles a GitCD object
type GitCDReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=git.winlab.com,resources=gitcds,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=git.winlab.com,resources=gitcds/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=git.winlab.com,resources=gitcds/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=pods,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GitCD object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *GitCDReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("run reconcile")
	gitcd := &gitv1alpha1.GitCD{}
	err := r.Get(ctx, req.NamespacedName, gitcd)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("GitCD resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get GitCD")
		return ctrl.Result{}, err
	}
	base := gitcd.Spec.Url
	repo, err := git.Clone(memory.NewStorage(), nil, &git.CloneOptions{
		URL: "https://" + base,
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	ref, err := repo.Head()
	if err != nil {
		return ctrl.Result{}, err
	}
	hash := ref.Hash()
	log.Info("Commit ID", "ID:", hash.String())
	found := &corev1.Pod{}
	err = r.Get(ctx, types.NamespacedName{Name: gitcd.Name + "-" + hash.String(), Namespace: gitcd.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		pod := r.MonitorGitRepositiry(gitcd, hash.String())
		log.Info("Start kaniko pod", "Pod.Namespace", pod.Namespace, "Pod.Name", pod.Name)
		err = r.Create(ctx, pod)
		if err != nil {
			log.Error(err, "Failed to create new Pod", "Pod.Namespace", pod.Namespace, "Pod.Name", pod.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Kaniko pod")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: time.Second * 5}, nil
}

func (r *GitCDReconciler) MonitorGitRepositiry(g *gitv1alpha1.GitCD, id string) *corev1.Pod {
	url := g.Spec.Url
	url = "git://" + url + "#refs/heads/master"
	tag := g.Spec.Tag
	pod := &corev1.Pod{

		ObjectMeta: metav1.ObjectMeta{
			Name:      g.Name + "-" + id,
			Namespace: g.Namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Image: "gcr.io/kaniko-project/executor:latest",
				Name:  "kaniko",
				Args:  []string{"--dockerfile=Dockerfile", "--context=" + url, "--destination=" + tag},
				VolumeMounts: []corev1.VolumeMount{{
					MountPath: "/kaniko/.docker",
					Name:      "kaniko-secret",
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "kaniko-secret",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "docker-basic",
						Items: []corev1.KeyToPath{{
							Key:  ".dockerconfigjson",
							Path: "config.json",
						}},
					},
				},
			}},
			RestartPolicy: "Never",
		},
	}
	ctrl.SetControllerReference(g, pod, r.Scheme)
	return pod
}

// SetupWithManager sets up the controller with the Manager.
func (r *GitCDReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gitv1alpha1.GitCD{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
