import 'package:flutter/material.dart';
import 'package:flutter_map/flutter_map.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:latlong2/latlong.dart' as ll;

import '../../models/models.dart';
import '../../providers/providers.dart';

/// Shows an order's status as it changes and, once a driver is assigned,
/// their live position — both delivered over the gateway's WebSocket
/// (services/gateway/internal/ws), with no polling.
class OrderTrackingScreen extends ConsumerStatefulWidget {
  final String orderId;
  const OrderTrackingScreen({super.key, required this.orderId});

  @override
  ConsumerState<OrderTrackingScreen> createState() => _OrderTrackingScreenState();
}

class _OrderTrackingScreenState extends ConsumerState<OrderTrackingScreen> {
  Order? _order;
  String _status = 'CREATED';
  LatLng2? _driverAt;
  int? _etaMinutes;

  @override
  void initState() {
    super.initState();
    ref.read(orderRepositoryProvider).get(widget.orderId).then((o) {
      if (mounted) {
        setState(() {
          _order = o;
          _status = o.status;
        });
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(orderEventsProvider(widget.orderId), (prev, next) {
      next.whenData(_handleEvent);
    });

    return Scaffold(
      appBar: AppBar(title: Text('Order #${widget.orderId.substring(0, 8)}')),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(16),
            child: _StatusStepper(status: _status),
          ),
          if (_etaMinutes != null)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: Text('Driver ETA: $_etaMinutes min', style: Theme.of(context).textTheme.titleMedium),
            ),
          Expanded(
            child: _driverAt == null
                ? Center(
                    child: _order == null
                        ? const CircularProgressIndicator()
                        : Text(_status == 'CANCELLED' ? 'This order was cancelled.' : 'Waiting for a driver...'),
                  )
                : FlutterMap(
                    options: MapOptions(
                      initialCenter: ll.LatLng(_driverAt!.lat, _driverAt!.lng),
                      initialZoom: 14,
                    ),
                    children: [
                      TileLayer(
                        urlTemplate: 'https://tile.openstreetmap.org/{z}/{x}/{y}.png',
                        userAgentPackageName: 'com.example.delivery_app',
                      ),
                      MarkerLayer(markers: [
                        Marker(
                          point: ll.LatLng(_driverAt!.lat, _driverAt!.lng),
                          width: 40,
                          height: 40,
                          child: const Icon(Icons.delivery_dining, color: Colors.deepOrange, size: 36),
                        ),
                      ]),
                    ],
                  ),
          ),
        ],
      ),
    );
  }

  void _handleEvent(Map<String, dynamic> env) {
    final type = env['eventType'] as String?;
    final data = env['data'] as Map<String, dynamic>?;
    if (data == null) return;
    setState(() {
      switch (type) {
        case 'jobs.status-changed':
          _status = data['to'] as String? ?? _status;
          break;
        case 'jobs.cancelled':
          _status = 'CANCELLED';
          break;
        case 'dispatch.location':
          _driverAt = LatLng2((data['lat'] as num).toDouble(), (data['lng'] as num).toDouble());
          _etaMinutes = (data['etaMinutes'] as num?)?.toInt();
          break;
      }
    });
  }
}

class _StatusStepper extends StatelessWidget {
  final String status;
  const _StatusStepper({required this.status});

  static const _steps = [
    'CREATED', 'CONFIRMED', 'PREPARING', 'READY', 'OUT_FOR_DELIVERY', 'DELIVERED',
  ];

  @override
  Widget build(BuildContext context) {
    if (status == 'CANCELLED') {
      return Row(children: [
        const Icon(Icons.cancel, color: Colors.red),
        const SizedBox(width: 8),
        Text('Cancelled', style: TextStyle(color: Theme.of(context).colorScheme.error)),
      ]);
    }
    final currentIndex = _steps.indexOf(status).clamp(0, _steps.length - 1);
    return Row(
      children: List.generate(_steps.length, (i) {
        final reached = i <= currentIndex;
        return Expanded(
          child: Column(
            children: [
              CircleAvatar(
                radius: 12,
                backgroundColor: reached ? Theme.of(context).colorScheme.primary : Colors.grey.shade300,
                child: reached ? const Icon(Icons.check, size: 14, color: Colors.white) : null,
              ),
              const SizedBox(height: 4),
              Text(
                _steps[i].replaceAll('_', ' '),
                textAlign: TextAlign.center,
                style: const TextStyle(fontSize: 9),
              ),
            ],
          ),
        );
      }),
    );
  }
}
