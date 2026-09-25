import 'dart:convert';
import 'dart:io';

import 'package:delivery_app/core/config.dart';
import 'package:delivery_app/l10n/app_localizations.dart';
import 'package:delivery_app/providers/providers.dart';
import 'package:delivery_app/screens/auth/login_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'brand_test.dart' show brandJson;

Map<String, dynamic> _arb(String locale) => jsonDecode(File('lib/l10n/app_$locale.arb').readAsStringSync()) as Map<String, dynamic>;

Set<String> _placeholders(String message) => RegExp(r'\{(\w+)[,}]').allMatches(message).map((m) => m.group(1)!).toSet();

void main() {
  // gen-l10n only warns about a missing translation and shows English in
  // its place. Here a missing or extra key fails, and so does a message
  // that drops or renames a placeholder (a noun that silently vanishes).
  test('every locale has every message, with the same placeholders', () {
    final en = _arb('en');
    final messages = en.keys.where((k) => !k.startsWith('@')).toSet();
    for (final locale in AppLocalizations.supportedLocales.map((l) => l.languageCode).where((l) => l != 'en')) {
      final other = _arb(locale);
      final keys = other.keys.where((k) => !k.startsWith('@')).toSet();
      expect(keys.difference(messages), isEmpty, reason: '$locale has keys English lacks');
      expect(messages.difference(keys), isEmpty, reason: '$locale is missing messages');
      for (final k in messages) {
        expect(_placeholders(other[k] as String), _placeholders(en[k] as String), reason: '$locale $k');
      }
    }
  });

  test('Afrikaans: plurals and number formatting', () {
    final af = lookupAppLocalizations(const Locale('af'));
    expect(af.viewCart(1), 'Sien mandjie (1 item)');
    expect(af.viewCart(3), 'Sien mandjie (3 items)');
    expect(af.minutes(12.5), '12,5 min'); // decimal comma
    expect(lookupAppLocalizations(const Locale('en')).minutes(12.5), '12.5 min');
  });

  testWidgets('the login screen speaks the locale the server chose', (tester) async {
    final config = AppConfig.fromJson({
      'brand': brandJson(),
      'locale': 'af',
      'pack': {'name': 'courier'},
      'roles': ['customer', 'agent', 'admin'],
      'modules': <String>[],
      'job': {'stops': 'point_to_point'},
      'lifecycle': {'states': <Map<String, dynamic>>[]},
    });
    await tester.pumpWidget(ProviderScope(
      overrides: [configProvider.overrideWith((ref) async => config)],
      child: const _Loaded(),
    ));
    await tester.pumpAndSettle();
    expect(find.text('Teken in'), findsOneWidget);
    expect(find.text("Nog nie 'n rekening nie? Registreer"), findsOneWidget);
    expect(find.text('Log in'), findsNothing);
    expect(find.text('Test Couriers'), findsOneWidget);
  });
}

/// The login screen under the app's localisation setup, once the config
/// is in (as main.dart builds it).
class _Loaded extends ConsumerWidget {
  const _Loaded();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final config = ref.watch(configProvider);
    if (!config.hasValue) return const SizedBox();
    return MaterialApp(
      locale: Locale(config.requireValue.locale),
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      supportedLocales: AppLocalizations.supportedLocales,
      home: const LoginScreen(),
    );
  }
}
