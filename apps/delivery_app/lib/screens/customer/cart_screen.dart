import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:geolocator/geolocator.dart';
import 'package:go_router/go_router.dart';

import '../../models/models.dart';
import '../../providers/providers.dart';

// Windhoek CBD, used as a fallback dropoff point whenever the device has no
// GPS fix (web, desktop, or a denied permission) — good enough for a demo.
const _fallbackLocation = LatLng2(-22.5700, 17.0836);

class CartScreen extends ConsumerStatefulWidget {
  const CartScreen({super.key});

  @override
  ConsumerState<CartScreen> createState() => _CartScreenState();
}

class _CartScreenState extends ConsumerState<CartScreen> {
  String _paymentMethod = 'card';
  bool _placing = false;
  String? _error;

  @override
  Widget build(BuildContext context) {
    final cart = ref.watch(cartProvider);
    final lines = cart.values.toList();
    final subtotal = ref.read(cartProvider.notifier).totalCents;

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
                        trailing: Text('N\$${(line.subtotalCents / 100).toStringAsFixed(2)}'),
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
                          Text('N\$${(subtotal / 100).toStringAsFixed(2)}'),
                        ],
                      ),
                      const SizedBox(height: 4),
                      const Text('Delivery fee (with any surge pricing) is calculated when the order is placed.',
                          style: TextStyle(fontSize: 12, color: Colors.grey)),
                      const SizedBox(height: 12),
                      DropdownButtonFormField<String>(
                        initialValue: _paymentMethod,
                        decoration: const InputDecoration(labelText: 'Payment method'),
                        items: const [
                          DropdownMenuItem(value: 'card', child: Text('Card')),
                          DropdownMenuItem(value: 'mobile-money', child: Text('Mobile money')),
                          DropdownMenuItem(value: 'cash', child: Text('Cash on delivery')),
                        ],
                        onChanged: (v) => setState(() => _paymentMethod = v!),
                      ),
                      if (_error != null) ...[
                        const SizedBox(height: 8),
                        Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
                      ],
                      const SizedBox(height: 12),
                      FilledButton(
                        onPressed: _placing ? null : () => _placeOrder(lines),
                        child: _placing
                            ? const SizedBox(height: 18, width: 18, child: CircularProgressIndicator(strokeWidth: 2))
                            : const Text('Place order'),
                      ),
                    ],
                  ),
                ),
              ],
            ),
    );
  }

  Future<void> _placeOrder(List<CartLine> lines) async {
    setState(() {
      _placing = true;
      _error = null;
    });
    try {
      final cartController = ref.read(cartProvider.notifier);
      final dropoff = await _currentLocationOrFallback();

      final order = await ref.read(orderRepositoryProvider).create(
            restaurantId: cartController.restaurantId!,
            lines: lines,
            paymentMethod: _paymentMethod,
            dropoff: dropoff,
          );

      cartController.clear();
      if (!mounted) return;
      context.go('/orders/${order.id}/track');
    } catch (e) {
      setState(() => _error = 'Could not place order: $e');
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
