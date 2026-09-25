// YOUR FILE (ADR-0004): an upgrade never changes it. The counterpart of
// cmd/platform/extensions.go for the app.
//
// Add your own modules here. A module implements AppModule
// (lib/core/module.dart) and lives in its own folder, e.g.
// lib/ext/returns/returns_module.dart; it may import lib/core and
// lib/l10n, never another module. `platformctl new-module <name> --app`
// scaffolds one and adds a line at each marker below; keep the markers.
import 'core/module.dart';
// platformctl:imports

const List<AppModule> extensionModules = [
  // platformctl:modules
];
