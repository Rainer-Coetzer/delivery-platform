import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:geolocator/geolocator.dart';

import '../../providers/providers.dart';

/// A driver's own screen: go online/offline and, once assigned, stream real
/// GPS to the delivery service (overriding its simulated movement — see
/// services/delivery/internal/api reportLocation) and mark pickup/delivered.
class DriverHomeScreen extends ConsumerStatefulWidget {
  const DriverHomeScreen({super.key});

  @override
  ConsumerState<DriverHomeScreen> createState() => _DriverHomeScreenState();
}

class _DriverHomeScreenState extends ConsumerState<DriverHomeScreen> {
  bool _online = false;
  Timer? _gpsTimer;
  String? _driverId;

  @override
  void dispose() {
    _gpsTimer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final user = ref.watch(authControllerProvider).user!;
    _driverId ??= user.refId.isNotEmpty ? user.refId : null;

    return Scaffold(
      appBar: AppBar(
        title: const Text('Driver'),
        actions: [
          IconButton(icon: const Icon(Icons.logout), onPressed: () => ref.read(authControllerProvider.notifier).logout()),
        ],
      ),
      body: _driverId == null
          ? const _RegisterDriverPrompt()
          : Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  SwitchListTile(
                    title: Text(_online ? 'Online — you can receive deliveries' : 'Offline'),
                    value: _online,
                    onChanged: _toggleOnline,
                  ),
                  const SizedBox(height: 24),
                  const Text(
                    'When a restaurant marks an order ready, the nearest available '
                    'driver is assigned automatically. While online, this screen '
                    'streams your GPS location so customers can see you on the map.',
                    style: TextStyle(color: Colors.grey),
                  ),
                ],
              ),
            ),
    );
  }

  Future<void> _toggleOnline(bool value) async {
    final repo = ref.read(deliveryRepositoryProvider);
    setState(() => _online = value);
    await repo.setStatus(_driverId!, value ? 'AVAILABLE' : 'OFFLINE');
    if (value) {
      _gpsTimer = Timer.periodic(const Duration(seconds: 5), (_) => _pushLocation());
      _pushLocation();
    } else {
      _gpsTimer?.cancel();
    }
  }

  Future<void> _pushLocation() async {
    try {
      final permission = await Geolocator.checkPermission();
      if (permission == LocationPermission.denied) {
        await Geolocator.requestPermission();
      }
      final pos = await Geolocator.getCurrentPosition().timeout(const Duration(seconds: 5));
      await ref.read(deliveryRepositoryProvider).reportLocation(_driverId!, pos.latitude, pos.longitude);
    } catch (_) {
      // No GPS available (desktop/web without location) — the delivery
      // service's own simulation keeps moving the driver instead.
    }
  }
}

/// Shown when a driver account isn't linked to a driver record yet. Drivers
/// can't create their own record any more: an admin onboards them (creates
/// the driver, then this login with refId set), so nobody can claim to be
/// a driver they aren't.
class _RegisterDriverPrompt extends StatelessWidget {
  const _RegisterDriverPrompt();

  @override
  Widget build(BuildContext context) {
    return const Center(
      child: Padding(
        padding: EdgeInsets.all(24),
        child: Text(
          "Your account isn't linked to a driver profile yet. Ask an admin to finish setting you up, then log in again.",
          textAlign: TextAlign.center,
        ),
      ),
    );
  }
}
