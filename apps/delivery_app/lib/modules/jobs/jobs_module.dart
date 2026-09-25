import 'package:go_router/go_router.dart';

import '../../core/config.dart';
import '../../core/module.dart';
import 'job_history_screen.dart';
import 'job_tracking_screen.dart';

/// A customer's jobs and following one live. Every platform has it: jobs
/// are the core. Customers start here when there is no catalogue to
/// browse (booking a courier or freight job in the app is Phase E).
class JobsModule extends AppModule {
  const JobsModule();

  @override
  String get name => 'jobs';

  @override
  bool enabledFor(AppConfig config) => true;

  @override
  List<RouteBase> routes(AppConfig config) => [
        GoRoute(path: '/jobs', builder: (context, state) => const JobHistoryScreen()),
        GoRoute(path: '/jobs/:id/track', builder: (context, state) => JobTrackingScreen(jobId: state.pathParameters['id']!)),
      ];

  @override
  String? homeFor(String role, AppConfig config) => role == 'customer' ? '/jobs' : null;
}
