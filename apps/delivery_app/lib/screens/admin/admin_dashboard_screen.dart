import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/providers.dart';

class AdminDashboardScreen extends ConsumerWidget {
  const AdminDashboardScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final config = ref.watch(configProvider).requireValue;
    return Scaffold(
      appBar: AppBar(
        title: const Text('Admin overview'),
        actions: [
          IconButton(icon: const Icon(Icons.logout), onPressed: () => ref.read(authControllerProvider.notifier).logout()),
        ],
      ),
      body: FutureBuilder<Map<String, dynamic>>(
        future: ref.read(adminRepositoryProvider).overview(),
        builder: (context, snapshot) {
          if (snapshot.hasError) return Center(child: Text('Could not load the overview: ${snapshot.error}'));
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
                  _StatCard(label: 'Total ${config.term('jobs')}', value: '${data['totalJobs'] ?? 0}'),
                  _StatCard(label: 'Total revenue', value: config.money((data['totalRevenueCents'] ?? 0) as num)),
                  _StatCard(
                    label: 'Average time to complete',
                    value: '${((data['avgCompletionMinutes'] ?? 0) as num).toStringAsFixed(1)} min',
                  ),
                ],
              ),
              const SizedBox(height: 24),
              Text('${config.title('jobs')} by state', style: Theme.of(context).textTheme.titleMedium),
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
