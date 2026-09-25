// YOUR FILE (ADR-0004): an upgrade never changes it.
//
// Your own modules, run by the platform binary after the shipped ones, and
// blank imports for packages that register strategies (pkg/strategy/*).
// `platformctl new-module <name>` adds a line at each marker below; add
// your own lines the same way, and keep the markers.
package main

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	// platformctl:imports
)

var extensions = []app.Definition{
	// platformctl:definitions
}
