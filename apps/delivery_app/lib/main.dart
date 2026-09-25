import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import 'app_modules.dart';
import 'core/brand.dart';
import 'core/config.dart';
import 'core/module.dart';
import 'core/providers.dart';
import 'core/session.dart';
import 'l10n/app_localizations.dart';

void main() {
  runApp(ProviderScope(
    overrides: [appModulesProvider.overrideWithValue(appModules)],
    child: const DeliveryApp(),
  ));
}

class DeliveryApp extends ConsumerWidget {
  const DeliveryApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Every screen speaks the pack's words in the brand's colours, so
    // nothing is shown until the platform config (GET /config) is in. The
    // spinner and the error before that use Material's neutral theme:
    // there is no brand yet to use.
    final theme = ThemeData(useMaterial3: true);
    // The app's strings (lib/l10n). Before the config is in, Flutter picks
    // from the device's language; after, the server's choice decides.
    const delegates = AppLocalizations.localizationsDelegates;
    const locales = AppLocalizations.supportedLocales;
    return ref.watch(configProvider).when(
          data: (config) {
            // Rebuilding GoRouter on every auth change is a simplification:
            // it only happens at login/logout, which is rare enough that
            // resetting the navigation stack then is what we want anyway
            // (the user lands on their role's home screen).
            final auth = ref.watch(authControllerProvider);
            final modules = enabledModules(ref.watch(appModulesProvider), config);
            return MaterialApp.router(
              title: config.brand.name,
              theme: buildTheme(config.brand),
              locale: Locale(config.locale),
              localizationsDelegates: delegates,
              supportedLocales: locales,
              // No animation from the neutral loading theme to the brand's:
              // it would flash Material purple into the brand's colours on
              // every start.
              themeAnimationDuration: Duration.zero,
              routerConfig: _buildRouter(auth, config, modules),
            );
          },
          loading: () => MaterialApp(
            theme: theme,
            localizationsDelegates: delegates,
            supportedLocales: locales,
            home: const Scaffold(body: Center(child: CircularProgressIndicator())),
          ),
          error: (e, _) => MaterialApp(
            theme: theme,
            localizationsDelegates: delegates,
            supportedLocales: locales,
            home: Builder(builder: (context) {
              final l10n = AppLocalizations.of(context);
              return Scaffold(
                body: Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Column(mainAxisSize: MainAxisSize.min, children: [
                      Text(l10n.cantReachPlatform('$e'), textAlign: TextAlign.center),
                      const SizedBox(height: 12),
                      FilledButton(onPressed: () => ref.invalidate(configProvider), child: Text(l10n.tryAgain)),
                    ]),
                  ),
                ),
              );
            }),
          ),
        );
  }

  /// The screens are whatever the enabled modules add (lib/core/module.dart);
  /// a platform without a catalogue has no /merchants at all.
  GoRouter _buildRouter(AuthState auth, AppConfig config, List<AppModule> modules) {
    return GoRouter(
      initialLocation: '/login',
      redirect: (context, state) {
        final loggedIn = auth.isLoggedIn;
        final loggingIn = state.matchedLocation == '/login' || state.matchedLocation == '/register';
        if (!loggedIn && !loggingIn) return '/login';
        if (loggedIn && loggingIn) return homeFor(modules, auth.user!.role, config);
        return null;
      },
      routes: [for (final m in modules) ...m.routes(config)],
    );
  }
}
