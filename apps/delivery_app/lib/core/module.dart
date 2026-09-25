// A module is one part of the app that a platform may or may not have. It
// mirrors the backend's modules: a capability the pack enables (catalogue)
// or a role the brand offers (merchant, agent). main.dart asks each module
// whether this platform has it and builds the app from the ones that say
// yes: their routes, each role's home, the merchant board's tabs.
//
// The rule is the backend's: modules never import each other. They share
// only lib/core and lib/l10n. When one module shows something from
// another (the catalogue's editor on the merchant board), it goes through
// a hook here, not an import (test/modules_test.dart checks the imports).
//
// This interface is a stable surface (ADR-0004): a buyer's module in
// lib/extensions.dart implements it, and it only grows by adding members
// with defaults.
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import 'config.dart';

abstract class AppModule {
  const AppModule();

  /// A short name, for logs and tests (the backend module's name where
  /// there is one).
  String get name;

  /// Whether this platform has the module.
  bool enabledFor(AppConfig config);

  /// The screens the module adds. Paths must not collide with another
  /// module's.
  List<RouteBase> routes(AppConfig config);

  /// Where someone with this role starts, if this module is their home.
  /// The first module in the list that answers wins.
  String? homeFor(String role, AppConfig config) => null;

  /// Tabs the module adds to the merchant board, after the job queue.
  List<MerchantTab> merchantTabs(AppConfig config) => const [];

  /// Links the module adds to the admin overview, to its own screens.
  List<ModuleLink> adminLinks(AppConfig config) => const [];
}

/// A link to a module's screen: a route path it adds.
class ModuleLink {
  final String label;
  final String path;
  final IconData icon;
  const ModuleLink({required this.label, required this.path, required this.icon});
}

/// A tab on the merchant board. The label is usually a pack word
/// (config.title('catalogue')).
class MerchantTab {
  final String label;
  final Widget Function(String merchantId) build;
  const MerchantTab({required this.label, required this.build});
}

/// Every module the app knows (ours, then the buyer's), set in main.dart.
/// Read it with enabledModules to get the ones this platform has.
final appModulesProvider = Provider<List<AppModule>>((ref) {
  throw StateError('appModulesProvider is set in main.dart; a test must override it');
});

List<AppModule> enabledModules(List<AppModule> all, AppConfig config) => all.where((m) => m.enabledFor(config)).toList();

/// Where a role starts: the first enabled module that claims it, else the
/// job list, which every platform has.
String homeFor(List<AppModule> enabled, String role, AppConfig config) {
  for (final m in enabled) {
    final home = m.homeFor(role, config);
    if (home != null) return home;
  }
  return '/jobs';
}
