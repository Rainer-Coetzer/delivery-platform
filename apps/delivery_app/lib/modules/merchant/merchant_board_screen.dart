import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/config.dart';
import '../../core/module.dart';
import '../../core/providers.dart';
import '../../core/session.dart';
import '../../l10n/app_localizations.dart';
import 'merchant_api.dart';

/// A merchant's own board: the live job queue (mark a job ready once it's
/// prepared; accept or reject already happen automatically in the merchant
/// module, on stock and opening hours), then a tab for each module that
/// adds one (the catalogue's editor, when the pack has a catalogue). Words
/// come from the pack: "Orders" and "Menu" for a restaurant.
class MerchantBoardScreen extends ConsumerStatefulWidget {
  const MerchantBoardScreen({super.key});

  @override
  ConsumerState<MerchantBoardScreen> createState() => _MerchantBoardScreenState();
}

class _MerchantBoardScreenState extends ConsumerState<MerchantBoardScreen> {
  @override
  Widget build(BuildContext context) {
    final user = ref.watch(authControllerProvider).user!;
    final config = ref.watch(configProvider).requireValue;
    final tabs = [for (final m in enabledModules(ref.watch(appModulesProvider), config)) ...m.merchantTabs(config)];
    final merchantId = user.refId;
    final l10n = AppLocalizations.of(context);

    if (merchantId.isEmpty) {
      return Scaffold(body: Center(child: Text(l10n.noMerchantProfile(config.term('merchant')))));
    }

    return DefaultTabController(
      length: 1 + tabs.length,
      child: Scaffold(
        appBar: AppBar(
          title: Text(config.title('merchant')),
          bottom: TabBar(tabs: [Tab(text: config.title('jobs')), for (final t in tabs) Tab(text: t.label)]),
          actions: [
            IconButton(icon: const Icon(Icons.logout), tooltip: l10n.logOut, onPressed: () => ref.read(authControllerProvider.notifier).logout()),
          ],
        ),
        body: TabBarView(
          children: [
            _JobQueue(merchantId: merchantId, config: config),
            for (final t in tabs) t.build(merchantId),
          ],
        ),
      ),
    );
  }
}

class _JobQueue extends ConsumerWidget {
  final String merchantId;
  final AppConfig config;
  const _JobQueue({required this.merchantId, required this.config});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final jobsAsync = ref.watch(merchantJobsProvider(merchantId));
    final l10n = AppLocalizations.of(context);
    return jobsAsync.when(
      data: (all) {
        final jobs = all.where((j) => !j.finished).toList();
        if (jobs.isEmpty) {
          return RefreshIndicator(
            onRefresh: () async => ref.invalidate(merchantJobsProvider(merchantId)),
            child: ListView(children: [Padding(padding: const EdgeInsets.all(32), child: Center(child: Text(l10n.noneActive(config.term('jobs')))))]),
          );
        }
        return RefreshIndicator(
          onRefresh: () async => ref.invalidate(merchantJobsProvider(merchantId)),
          child: ListView.builder(
            itemCount: jobs.length,
            itemBuilder: (context, i) {
              final j = jobs[i];
              return Card(
                margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                child: ListTile(
                  title: Text(l10n.jobNumberState(config.title('job'), j.id.substring(0, 8), config.stateLabel(j.status))),
                  subtitle: Text(j.items.map((it) => '${it.quantity}x ${it.name}').join(', ')),
                  // Once confirmed (paid and accepted) the merchant prepares
                  // it and says when it's ready; the milestone says when,
                  // whatever the pack calls the state.
                  trailing: j.milestone == 'confirmed'
                      ? FilledButton(
                          onPressed: () async {
                            await ref.read(merchantRepositoryProvider).markReady(merchantId, j.id);
                            ref.invalidate(merchantJobsProvider(merchantId));
                          },
                          child: Text(l10n.markReady),
                        )
                      : null,
                ),
              );
            },
          ),
        );
      },
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, st) => Center(child: Text(l10n.couldNotLoad(config.term('jobs'), '$e'))),
    );
  }
}
