import 'dart:io';

import 'package:delivery_app/app_modules.dart';
import 'package:delivery_app/core/config.dart';
import 'package:delivery_app/core/module.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

import 'brand_test.dart' show brandJson;

AppConfig platform({required List<String> modules, required List<String> roles}) => AppConfig.fromJson({
      'brand': brandJson(),
      'locale': 'en',
      'pack': {'name': 'test'},
      'roles': roles,
      'modules': modules,
      'job': {'stops': 'point_to_point'},
      'lifecycle': {'states': <Map<String, dynamic>>[]},
      'terms': {'catalogue': 'price list'},
    });

Set<String> paths(List<AppModule> modules, AppConfig config) => {
      for (final m in modules)
        for (final r in m.routes(config)) (r as GoRoute).path
    };

void main() {
  final withCatalogue = platform(modules: ['catalogue'], roles: ['customer', 'merchant', 'agent', 'admin']);
  final courier = platform(modules: ['packages', 'proof'], roles: ['customer', 'agent', 'admin']);
  final staffOnly = platform(modules: ['catalogue'], roles: ['merchant', 'agent', 'admin']);

  test('a platform with a catalogue: browse it, merchants edit it', () {
    final on = enabledModules(appModules, withCatalogue);
    expect(paths(on, withCatalogue), containsAll(['/login', '/register', '/merchants', '/cart', '/jobs', '/merchant-board', '/agent', '/admin']));
    expect(homeFor(on, 'customer', withCatalogue), '/merchants');
    expect(homeFor(on, 'merchant', withCatalogue), '/merchant-board');
    final tabs = [for (final m in on) ...m.merchantTabs(withCatalogue)];
    expect(tabs.map((t) => t.label), ['Price list']);
  });

  test('without a catalogue or merchants, their screens are not there at all', () {
    final on = enabledModules(appModules, courier);
    final p = paths(on, courier);
    expect(p, isNot(contains('/merchants')));
    expect(p, isNot(contains('/cart')));
    expect(p, isNot(contains('/merchant-board')));
    expect(homeFor(on, 'customer', courier), '/jobs');
    expect(homeFor(on, 'agent', courier), '/agent');
  });

  test('no customer role: no sign-up screen', () {
    final on = enabledModules(appModules, staffOnly);
    expect(paths(on, staffOnly), isNot(contains('/register')));
  });

  test('no two modules claim the same path', () {
    final seen = <String>{};
    for (final m in appModules) {
      for (final r in m.routes(withCatalogue)) {
        expect(seen.add((r as GoRoute).path), isTrue, reason: '${m.name} repeats ${r.path}');
      }
    }
  });

  // The backend's rule, in the app: a module may import lib/core, lib/l10n
  // and its own files, never another module. Sharing goes through
  // AppModule's hooks (lib/core/module.dart).
  test('modules never import each other', () {
    final problems = <String>[];
    for (final dir in [Directory('lib/modules'), Directory('lib/ext')].where((d) => d.existsSync())) {
      for (final f in dir.listSync(recursive: true).whereType<File>().where((f) => f.path.endsWith('.dart'))) {
        final parts = f.uri.pathSegments;
        final own = parts[parts.indexOf(dir.uri.pathSegments[dir.uri.pathSegments.length - 2]) + 1];
        for (final m in RegExp(r"""import\s+'([^']+)'""").allMatches(f.readAsStringSync())) {
          final target = m.group(1)!;
          final crossesOver = RegExp(r'(^|/)(modules|ext)/([^/]+)/').firstMatch(target) ?? RegExp(r'^\.\./([^/.][^/]*)/').firstMatch(target);
          if (crossesOver == null) continue;
          final other = crossesOver.groupCount == 3 ? crossesOver.group(3)! : crossesOver.group(1)!;
          if (other != own) problems.add('${f.path}: imports $target');
        }
      }
    }
    expect(problems, isEmpty);
  });
}
