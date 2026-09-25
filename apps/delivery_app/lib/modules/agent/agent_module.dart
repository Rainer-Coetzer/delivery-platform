import 'package:go_router/go_router.dart';

import '../../core/config.dart';
import '../../core/module.dart';
import 'agent_home_screen.dart';

/// An agent's screen: going online and streaming their position (dispatch). Only when the brand offers the agent role.
class AgentModule extends AppModule {
  const AgentModule();

  @override
  String get name => 'agent';

  @override
  bool enabledFor(AppConfig config) => config.roles.contains('agent');

  @override
  List<RouteBase> routes(AppConfig config) => [
        GoRoute(path: '/agent', builder: (context, state) => const AgentHomeScreen()),
      ];

  @override
  String? homeFor(String role, AppConfig config) => role == 'agent' ? '/agent' : null;
}
