import 'package:delivery_app/app_modules.dart';
import 'package:delivery_app/core/api_client.dart';
import 'package:delivery_app/core/config.dart';
import 'package:delivery_app/core/models.dart';
import 'package:delivery_app/core/module.dart';
import 'package:delivery_app/core/providers.dart';
import 'package:delivery_app/core/session.dart';
import 'package:delivery_app/l10n/app_localizations.dart';
import 'package:delivery_app/modules/catalogue/catalogue_api.dart';
import 'package:delivery_app/modules/merchant/merchant_api.dart';
import 'package:delivery_app/modules/merchant/merchant_board_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'modules_test.dart' show platform;

/// A merchant who is already signed in.
class _SignedIn extends AuthController {
  _SignedIn() : super(ApiClient(baseUrl: 'http://test.invalid')) {
    state = AuthState(user: User(id: 'u1', name: 'Kapana staff', email: 'k@example.com', role: 'merchant', refId: 'm1'));
  }
}

Future<void> pumpBoard(WidgetTester tester, AppConfig config) async {
  await tester.pumpWidget(ProviderScope(
    overrides: [
      appModulesProvider.overrideWithValue(appModules),
      configProvider.overrideWith((ref) async => config),
      authControllerProvider.overrideWith((ref) => _SignedIn()),
      merchantJobsProvider.overrideWith((ref, id) async => <Job>[]),
      catalogueProvider.overrideWith((ref, id) async => []),
    ],
    child: Consumer(builder: (context, ref, _) {
      if (!ref.watch(configProvider).hasValue) return const SizedBox();
      return const MaterialApp(
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        home: MerchantBoardScreen(),
      );
    }),
  ));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('with a catalogue, the board has its editor as a second tab', (tester) async {
    await pumpBoard(tester, platform(modules: ['catalogue'], roles: ['customer', 'merchant']));
    expect(find.byType(Tab), findsNWidgets(2));
    expect(find.text('Price list'), findsOneWidget); // the catalogue module's tab, in the pack's word
    await tester.tap(find.text('Price list'));
    await tester.pumpAndSettle();
    expect(find.text('Price'), findsOneWidget); // the editor's form
  });

  testWidgets('without one, the board is just the job queue', (tester) async {
    await pumpBoard(tester, platform(modules: ['approval'], roles: ['customer', 'merchant']));
    expect(find.byType(Tab), findsOneWidget);
    expect(find.text('Price list'), findsNothing);
  });
}
