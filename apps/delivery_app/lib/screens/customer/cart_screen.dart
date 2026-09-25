import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:geolocator/geolocator.dart';
import 'package:go_router/go_router.dart';

import '../../core/config.dart';
import '../../models/models.dart';
import '../../providers/providers.dart';

// Windhoek CBD, used as a fallback dropoff point whenever the device has no
// GPS fix (web, desktop, or a denied permission): good enough for a demo.
// Known debt: moves to the platform profile's default location (Phase D).
const _fallbackLocation = LatLng2(-22.5700, 17.0836);

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

    return Scaffold(
      appBar: AppBar(title: const Text('Your cart')),
      body: lines.isEmpty
          ? const Center(child: Text('Your cart is empty'))
          : Column(
              children: [
                Expanded(
                  child: ListView.builder(
                    itemCount: lines.length,
                    itemBuilder: (context, i) {
                      final line = lines[i];
                      return ListTile(
                        title: Text(line.item.name),
                        subtitle: Text('x${line.quantity}'),
                        trailing: Text(money(line.subtotalCents)),
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
                          const Text('Subtotal'),
                          Text(money(subtotal)),
                        ],
                      ),
                      const SizedBox(height: 4),
                      Text('The delivery fee is worked out when the ${config.term('job')} is placed.',
                          style: const TextStyle(fontSize: 12, color: Colors.grey)),
                      const SizedBox(height: 12),
                      // Only the methods this platform takes (GET /config): the
                      // server refuses any other.
                      DropdownButtonFormField<String>(
                        initialValue: method,
                        decoration: const InputDecoration(labelText: 'Payment method'),
                        items: [
                          for (final m in methods) DropdownMenuItem(value: m, child: Text(_methodLabel(m))),
                        ],
                        onChanged: (v) => setState(() => _paymentMethod = v),
                      ),
                      if (_error != null) ...[
                        const SizedBox(height: 8),
                        Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
                      ],
                      const SizedBox(height: 12),
                      FilledButton(
                        onPressed: _placing || method == null ? null : () => _place(lines, method, config),
                        child: _placing
                            ? const SizedBox(height: 18, width: 18, child: CircularProgressIndicator(strokeWidth: 2))
                            : Text('Place ${config.term('job')}'),
                      ),
                    ],
                  ),
                ),
              ],
            ),
    );
  }

  static String _methodLabel(String m) => switch (m) {
        'cash' => 'Cash on delivery',
        'card' => 'Card',
        _ => m.isEmpty ? m : m[0].toUpperCase() + m.substring(1).replaceAll('-', ' '),
      };

  Future<void> _place(List<CartLine> lines, String method, AppConfig config) async {
    setState(() {
      _placing = true;
      _error = null;
    });
    try {
      final cartController = ref.read(cartProvider.notifier);
      final dropoff = await _currentLocationOrFallback();

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
      setState(() => _error = 'Could not place the ${config.term('job')}: $e');
    } finally {
      if (mounted) setState(() => _placing = false);
    }
  }

  Future<LatLng2> _currentLocationOrFallback() async {
    try {
      final permission = await Geolocator.checkPermission();
      if (permission == LocationPermission.denied) {
        final requested = await Geolocator.requestPermission();
        if (requested == LocationPermission.denied || requested == LocationPermission.deniedForever) {
          return _fallbackLocation;
        }
      }
      if (!await Geolocator.isLocationServiceEnabled()) return _fallbackLocation;
      final pos = await Geolocator.getCurrentPosition().timeout(const Duration(seconds: 5));
      return LatLng2(pos.latitude, pos.longitude);
    } catch (_) {
      return _fallbackLocation;
    }
  }
}
