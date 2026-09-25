import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:geolocator/geolocator.dart';

import '../../providers/providers.dart';
import '../../l10n/app_localizations.dart';

/// An agent's own screen ("driver", "courier": the pack's word): go online
/// or offline and, while online, stream real GPS to dispatch (which then
/// stops simulating their movement; services/dispatch/internal/api
/// reportLocation). Working through stops and capturing proof is the agent
/// app (Phase E).
class AgentHomeScreen extends ConsumerStatefulWidget {
  const AgentHomeScreen({super.key});

  @override
  ConsumerState<AgentHomeScreen> createState() => _AgentHomeScreenState();
}

class _AgentHomeScreenState extends ConsumerState<AgentHomeScreen> {
  bool _online = false;
  Timer? _gpsTimer;
  String? _agentId;

  @override
  void dispose() {
    _gpsTimer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final user = ref.watch(authControllerProvider).user!;
    final config = ref.watch(configProvider).requireValue;
    _agentId ??= user.refId.isNotEmpty ? user.refId : null;
    final l10n = AppLocalizations.of(context);

    return Scaffold(
      appBar: AppBar(
        title: Text(config.title('agent')),
        actions: [
          IconButton(icon: const Icon(Icons.logout), tooltip: l10n.logOut, onPressed: () => ref.read(authControllerProvider.notifier).logout()),
        ],
      ),
      body: _agentId == null
          ? _NotLinkedPrompt(agent: config.term('agent'))
          : Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  SwitchListTile(
                    title: Text(_online ? l10n.onlineReceive(config.term('jobs')) : l10n.offline),
                    value: _online,
                    onChanged: _toggleOnline,
                  ),
                  const SizedBox(height: 24),
                  Text(
                    l10n.agentHelp(config.term('job'), config.term('agent')),
                    style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
                  ),
                ],
              ),
            ),
    );
  }

  Future<void> _toggleOnline(bool value) async {
    final repo = ref.read(dispatchRepositoryProvider);
    setState(() => _online = value);
    await repo.setStatus(_agentId!, value ? 'AVAILABLE' : 'OFFLINE');
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
      await ref.read(dispatchRepositoryProvider).reportLocation(_agentId!, pos.latitude, pos.longitude);
    } catch (_) {
      // No GPS here (desktop, or web without location): dispatch's own
      // simulation keeps moving the agent instead.
    }
  }
}

/// Shown when an agent login isn't linked to an agent record yet. Agents
/// can't create their own record: an admin onboards them (creates the
/// agent, then this login with refId set), so nobody can claim to be an
/// agent they aren't.
class _NotLinkedPrompt extends StatelessWidget {
  final String agent;
  const _NotLinkedPrompt({required this.agent});

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Text(
          AppLocalizations.of(context).agentNotLinked(agent),
          textAlign: TextAlign.center,
        ),
      ),
    );
  }
}
