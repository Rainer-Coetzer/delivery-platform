import 'package:flutter/material.dart';
import 'package:flutter_map/flutter_map.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:latlong2/latlong.dart' as ll;

import '../../core/config.dart';
import '../../core/jobs.dart';
import '../../core/models.dart';
import '../../core/providers.dart';
import '../../l10n/app_localizations.dart';

/// Shows a job's state as it changes and, once an agent is assigned, their
/// live position, both over the gateway's WebSocket
/// (services/gateway/internal/ws), with no polling. The progress steps are
/// the pack's lifecycle states, labelled by the pack: nothing here knows
/// what food, courier or freight states are called.
class JobTrackingScreen extends ConsumerStatefulWidget {
  final String jobId;
  const JobTrackingScreen({super.key, required this.jobId});

  @override
  ConsumerState<JobTrackingScreen> createState() => _JobTrackingScreenState();
}

class _JobTrackingScreenState extends ConsumerState<JobTrackingScreen> {
  Job? _job;
  String? _status; // the pack's state name
  LatLng2? _agentAt;
  int? _etaMinutes;

  @override
  void initState() {
    super.initState();
    ref.read(jobRepositoryProvider).get(widget.jobId).then((j) {
      if (mounted) {
        setState(() {
          _job = j;
          _status = j.status;
        });
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final config = ref.watch(configProvider).requireValue;
    ref.listen(jobEventsProvider(widget.jobId), (prev, next) {
      next.whenData(_handleEvent);
    });
    final cancelled = _status != null && config.milestoneOf(_status!) == 'cancelled';
    final l10n = AppLocalizations.of(context);

    return Scaffold(
      appBar: AppBar(title: Text(l10n.jobNumber(config.title('job'), widget.jobId.substring(0, 8)))),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(16),
            child: _status == null ? const LinearProgressIndicator() : _Progress(config: config, status: _status!),
          ),
          if (_etaMinutes != null && !cancelled)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: Text(l10n.agentArrivesIn(config.title('agent'), _etaMinutes!), style: Theme.of(context).textTheme.titleMedium),
            ),
          Expanded(
            child: _agentAt == null
                ? Center(
                    child: _job == null
                        ? const CircularProgressIndicator()
                        : Text(cancelled ? l10n.jobCancelled(config.term('job')) : l10n.waitingForAgent(config.term('agent'))),
                  )
                : FlutterMap(
                    options: MapOptions(
                      initialCenter: ll.LatLng(_agentAt!.lat, _agentAt!.lng),
                      // The brand's zoom frames a city; following one agent
                      // wants a street-level view, so never less than 14.
                      initialZoom: config.brand.map.zoom < 14 ? 14 : config.brand.map.zoom,
                      maxZoom: config.brand.map.maxZoom.toDouble(),
                    ),
                    children: [
                      // The brand's tile provider (docs/MAPS.md).
                      TileLayer(
                        urlTemplate: config.brand.map.tilesUrl,
                        maxZoom: config.brand.map.maxZoom.toDouble(),
                        userAgentPackageName: 'com.example.delivery_app',
                      ),
                      MarkerLayer(markers: [
                        Marker(
                          point: ll.LatLng(_agentAt!.lat, _agentAt!.lng),
                          width: 40,
                          height: 40,
                          child: Icon(Icons.navigation, color: Theme.of(context).colorScheme.primary, size: 36),
                        ),
                      ]),
                      // Always on screen: every tile provider's terms ask for it.
                      SimpleAttributionWidget(source: Text(config.brand.map.attribution)),
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
        case 'dispatch.location':
          _agentAt = LatLng2((data['lat'] as num).toDouble(), (data['lng'] as num).toDouble());
          _etaMinutes = (data['etaMinutes'] as num?)?.toInt();
          break;
      }
    });
  }
}

/// The job's way through its lifecycle, one step per state the pack
/// defines, or a single "cancelled" line if that's where it ended.
class _Progress extends StatelessWidget {
  final AppConfig config;
  final String status;
  const _Progress({required this.config, required this.status});

  @override
  Widget build(BuildContext context) {
    if (config.milestoneOf(status) == 'cancelled') {
      return Row(children: [
        Icon(Icons.cancel, color: Theme.of(context).colorScheme.error),
        const SizedBox(width: 8),
        Text(config.stateLabel(status), style: TextStyle(color: Theme.of(context).colorScheme.error)),
      ]);
    }
    final steps = config.progress;
    final current = steps.indexWhere((s) => s.name == status);
    return Row(
      children: List.generate(steps.length, (i) {
        final reached = current >= 0 && i <= current;
        final scheme = Theme.of(context).colorScheme;
        return Expanded(
          child: Semantics(
            label: reached ? AppLocalizations.of(context).stepDone(steps[i].label) : steps[i].label,
            child: Column(
              children: [
                CircleAvatar(
                  radius: 12,
                  backgroundColor: reached ? scheme.primary : scheme.surfaceContainerHighest,
                  child: reached ? Icon(Icons.check, size: 14, color: scheme.onPrimary) : null,
                ),
                const SizedBox(height: 4),
                Text(steps[i].label, textAlign: TextAlign.center, style: const TextStyle(fontSize: 10)),
              ],
            ),
          ),
        );
      }),
    );
  }
}
