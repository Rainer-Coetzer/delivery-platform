import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/jobs.dart';
import '../../core/providers.dart';
import '../../core/session.dart';
import '../../l10n/app_localizations.dart';

/// A customer's jobs ("orders", "deliveries", "loads"), newest first, with
/// each one's state as the pack labels it.
class JobHistoryScreen extends ConsumerWidget {
  const JobHistoryScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final user = ref.watch(authControllerProvider).user!;
    final config = ref.watch(configProvider).requireValue;
    final l10n = AppLocalizations.of(context);

    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.yourJobs(config.term('jobs'))),
        actions: [
          if (config.has('catalogue'))
            IconButton(icon: const Icon(Icons.storefront), tooltip: config.title('merchants'), onPressed: () => context.go('/merchants')),
          IconButton(
            icon: const Icon(Icons.logout),
            tooltip: l10n.logOut,
            onPressed: () => ref.read(authControllerProvider.notifier).logout(),
          ),
        ],
      ),
      body: ref.watch(customerJobsProvider(user.id)).when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(child: Text(l10n.couldNotLoadYour(config.term('jobs'), '$e'))),
        data: (jobs) {
          if (jobs.isEmpty) return Center(child: Text(l10n.noneYet(config.term('jobs'))));
          return ListView.builder(
            itemCount: jobs.length,
            itemBuilder: (context, i) {
              final j = jobs[i];
              return ListTile(
                title: Text(j.merchantName.isNotEmpty ? j.merchantName : l10n.jobNumber(config.title('job'), j.id.substring(0, 8))),
                subtitle: Text('${config.money(j.totalCents)}: ${config.stateLabel(j.status)}'),
                trailing: const Icon(Icons.chevron_right),
                onTap: () => context.push('/jobs/${j.id}/track'),
              );
            },
          );
        },
      ),
    );
  }
}
