import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/providers.dart';
import '../../l10n/app_localizations.dart';

class AdminDashboardScreen extends ConsumerWidget {
  const AdminDashboardScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final config = ref.watch(configProvider).requireValue;
    final l10n = AppLocalizations.of(context);
    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.adminOverview),
        actions: [
          IconButton(icon: const Icon(Icons.logout), tooltip: l10n.logOut, onPressed: () => ref.read(authControllerProvider.notifier).logout()),
        ],
      ),
      body: FutureBuilder<Map<String, dynamic>>(
        future: ref.read(adminRepositoryProvider).overview(),
        builder: (context, snapshot) {
          if (snapshot.hasError) return Center(child: Text(l10n.couldNotLoadOverview('${snapshot.error}')));
          if (!snapshot.hasData) return const Center(child: CircularProgressIndicator());
          final data = snapshot.data!;
          final byStatus = (data['jobsByStatus'] as Map<String, dynamic>?) ?? {};
          return ListView(
            padding: const EdgeInsets.all(16),
            children: [
              Wrap(
                spacing: 12,
                runSpacing: 12,
                children: [
                  _StatCard(label: l10n.totalJobs(config.term('jobs')), value: '${data['totalJobs'] ?? 0}'),
                  _StatCard(label: l10n.totalRevenue, value: config.money((data['totalRevenueCents'] ?? 0) as num)),
                  _StatCard(
                    label: l10n.averageTimeToComplete,
                    value: l10n.minutes(((data['avgCompletionMinutes'] ?? 0) as num).toDouble()),
                  ),
                ],
              ),
              const SizedBox(height: 24),
              Text(l10n.jobsByState(config.title('jobs')), style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 8),
              ...byStatus.entries.map((e) => ListTile(title: Text(config.stateLabel(e.key)), trailing: Text('${e.value}'))),
            ],
          );
        },
      ),
    );
  }
}

class _StatCard extends StatelessWidget {
  final String label;
  final String value;
  const _StatCard({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(label, style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant)),
            const SizedBox(height: 4),
            Text(value, style: Theme.of(context).textTheme.headlineSmall),
          ],
        ),
      ),
    );
  }
}
