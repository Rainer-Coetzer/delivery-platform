import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:geolocator/geolocator.dart';
import 'package:go_router/go_router.dart';

import '../../core/config.dart';
import '../../models/models.dart';
import '../../providers/providers.dart';
import '../../l10n/app_localizations.dart';

class CartScreen extends ConsumerStatefulWidget {
  const CartScreen({super.key});

  @override
  ConsumerState<CartScreen> createState() => _CartScreenState();
}

class _CartScreenState extends ConsumerState<CartScreen> {
  String? _paymentMethod; // the first the platform takes, until chosen
  bool _placing = false;
  String? _error;

  @override
  Widget build(BuildContext context) {
    final cart = ref.watch(cartProvider);
    final lines = cart.values.toList();
    final subtotal = ref.read(cartProvider.notifier).totalCents;
    final config = ref.watch(configProvider).requireValue;
    final methods = config.paymentMethods;
    final method = _paymentMethod ?? (methods.isNotEmpty ? methods.first : null);
    final l10n = AppLocalizations.of(context);

    return Scaffold(
      appBar: AppBar(title: Text(l10n.yourCart)),
      body: lines.isEmpty
          ? Center(child: Text(l10n.cartEmpty))
          : Column(
              children: [
                Expanded(
                  child: ListView.builder(
                    itemCount: lines.length,
                    itemBuilder: (context, i) {
                      final line = lines[i];
                      return ListTile(
                        title: Text(line.item.name),
                        subtitle: Text(l10n.quantityTimes(line.quantity)),
                        trailing: Text(config.money(line.subtotalCents)),
                      );
                    },
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Row(
                        mainAxisAlignment: MainAxisAlignment.spaceBetween,
                        children: [
                          Text(l10n.subtotal),
                          Text(config.money(subtotal)),
                        ],
                      ),
                      const SizedBox(height: 4),
                      Text(l10n.feeWorkedOutLater(config.term('job')), style: TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant)),
                      const SizedBox(height: 12),
                      // Only the methods this platform takes (GET /config): the
                      // server refuses any other.
                      DropdownButtonFormField<String>(
                        initialValue: method,
                        decoration: InputDecoration(labelText: l10n.paymentMethod),
                        items: [
                          for (final m in methods) DropdownMenuItem(value: m, child: Text(_methodLabel(l10n, m))),
                        ],
                        onChanged: (v) => setState(() => _paymentMethod = v),
                      ),
                      if (_error != null) ...[
                        const SizedBox(height: 8),
                        Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
                      ],
                      const SizedBox(height: 12),
                      FilledButton(
                        onPressed: _placing || method == null ? null : () => _place(lines, method, config, l10n),
                        child: _placing
                            ? const SizedBox(height: 18, width: 18, child: CircularProgressIndicator(strokeWidth: 2))
                            : Text(l10n.placeJob(config.term('job'))),
                      ),
                    ],
                  ),
                ),
              ],
            ),
    );
  }

  /// The methods the app has words for; a buyer's own method shows its
  /// name, readable enough until they add a string for it.
  static String _methodLabel(AppLocalizations l10n, String m) => switch (m) {
        'cash' => l10n.payCash,
        'card' => l10n.payCard,
        _ => m.isEmpty ? m : m[0].toUpperCase() + m.substring(1).replaceAll('-', ' '),
      };

  Future<void> _place(List<CartLine> lines, String method, AppConfig config, AppLocalizations l10n) async {
    setState(() {
      _placing = true;
      _error = null;
    });
    try {
      final cartController = ref.read(cartProvider.notifier);
      final dropoff = await _currentLocationOrFallback(config);

      final job = await ref.read(jobRepositoryProvider).createFromCatalogue(
            merchantId: cartController.merchantId!,
            lines: lines,
            paymentMethod: method,
            dropoff: dropoff,
          );

      cartController.clear();
      if (!mounted) return;
      context.go('/jobs/${job.id}/track');
    } catch (e) {
      setState(() => _error = l10n.couldNotPlace(config.term('job'), '$e'));
    } finally {
      if (mounted) setState(() => _placing = false);
    }
  }

  /// Where to deliver: the device's position, or, with no GPS fix (web,
  /// desktop, a denied permission), the centre of the brand's map. Good
  /// enough for a demo; the customer app (Phase E) asks for an address.
  Future<LatLng2> _currentLocationOrFallback(AppConfig config) async {
    final fallback = LatLng2(config.brand.map.lat, config.brand.map.lng);
    try {
      final permission = await Geolocator.checkPermission();
      if (permission == LocationPermission.denied) {
        final requested = await Geolocator.requestPermission();
        if (requested == LocationPermission.denied || requested == LocationPermission.deniedForever) {
          return fallback;
        }
      }
      if (!await Geolocator.isLocationServiceEnabled()) return fallback;
      final pos = await Geolocator.getCurrentPosition().timeout(const Duration(seconds: 5));
      return LatLng2(pos.latitude, pos.longitude);
    } catch (_) {
      return fallback;
    }
  }
}
