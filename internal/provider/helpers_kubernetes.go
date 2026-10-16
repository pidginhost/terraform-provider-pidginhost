package provider

import "time"

// kubernetesPoll is how often the Kubernetes resources poll the API while
// they wait. Tests shorten it.
var kubernetesPoll = 5 * time.Second
