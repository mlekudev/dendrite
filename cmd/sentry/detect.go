package main

import (
	"github.com/mlekudev/dendrite/pkg/detect"
)

// Detector wraps the shared detect package for use in sentry.
// This thin wrapper preserves the existing sentry API surface.
type Detector = detect.Detector

// NewDetector creates a detector from trained mindsicles.
var NewDetector = detect.NewDetector
