import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/brand_logo.dart';
import '../../providers/providers.dart';
import '../../l10n/app_localizations.dart';

class LoginScreen extends ConsumerStatefulWidget {
  const LoginScreen({super.key});

  @override
  ConsumerState<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends ConsumerState<LoginScreen> {
  final _email = TextEditingController(text: 'nangula@example.com');
  final _password = TextEditingController(text: 'password123');
  String? _error;

  @override
  Widget build(BuildContext context) {
    final auth = ref.watch(authControllerProvider);
    final config = ref.watch(configProvider).requireValue;
    final text = Theme.of(context).textTheme;
    final l10n = AppLocalizations.of(context);

    return Scaffold(
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 400),
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Center(child: BrandLogo(brand: config.brand)),
                const SizedBox(height: 8),
                Text(config.brand.name, style: text.headlineSmall, textAlign: TextAlign.center),
                if (config.brand.tagline.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(config.brand.tagline,
                      style: text.bodyMedium?.copyWith(color: Theme.of(context).colorScheme.onSurfaceVariant), textAlign: TextAlign.center),
                ],
                const SizedBox(height: 24),
                TextField(controller: _email, decoration: InputDecoration(labelText: l10n.email)),
                const SizedBox(height: 12),
                TextField(controller: _password, obscureText: true, decoration: InputDecoration(labelText: l10n.password)),
                if (_error != null) ...[
                  const SizedBox(height: 12),
                  Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
                ],
                const SizedBox(height: 20),
                FilledButton(
                  onPressed: auth.loading ? null : _submit,
                  child: auth.loading ? const SizedBox(height: 18, width: 18, child: CircularProgressIndicator(strokeWidth: 2)) : Text(l10n.logIn),
                ),
                // Without a customer role there's no public sign-up
                // (identity refuses it too): accounts come from an admin.
                if (config.openSignUp)
                  TextButton(
                    onPressed: () => context.go('/register'),
                    child: Text(l10n.noAccountRegister),
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _submit() async {
    final error = await ref.read(authControllerProvider.notifier).login(_email.text.trim(), _password.text);
    if (!mounted) return;
    setState(() => _error = error == null || error.isNotEmpty ? error : AppLocalizations.of(context).somethingWentWrong);
  }
}
