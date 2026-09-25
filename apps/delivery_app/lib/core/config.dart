// The platform's configuration from GET /config: the brand (name, colours,
// currency, map), the pack's words, its lifecycle, its modules. Screens
// read nouns and state labels from here and never hardcode a business: the
// same app is a food app, a courier app or a freight app depending on the
// pack the server runs, under whatever name the buyer gave it.
import 'brand.dart';


class StateInfo {
  final String name;
  final String label;
  final String milestone; // '' when the state maps none (food's PREPARING)
  final bool terminal;

  const StateInfo({required this.name, required this.label, required this.milestone, required this.terminal});

  factory StateInfo.fromJson(Map<String, dynamic> json) => StateInfo(
        name: json['name'] as String,
        label: json['label'] as String? ?? json['name'] as String,
        milestone: json['milestone'] as String? ?? '',
        terminal: json['terminal'] as bool? ?? false,
      );
}

class AppConfig {
  final Brand brand;
  final String pack;
  final List<String> roles; // the roles this platform offers
  final List<String> modules;
  final String stopsTemplate; // merchant_to_customer, point_to_point, multi_stop
  final List<String> paymentMethods;
  final List<StateInfo> states;
  final Map<String, String> terms;

  const AppConfig({
    required this.brand,
    required this.pack,
    required this.roles,
    required this.modules,
    required this.stopsTemplate,
    required this.paymentMethods,
    required this.states,
    required this.terms,
  });

  factory AppConfig.fromJson(Map<String, dynamic> json) => AppConfig(
        brand: Brand.fromJson(json['brand'] as Map<String, dynamic>),
        pack: (json['pack'] as Map<String, dynamic>)['name'] as String,
        roles: List<String>.from(json['roles'] as List? ?? const []),
        modules: List<String>.from(json['modules'] as List? ?? const []),
        stopsTemplate: (json['job'] as Map<String, dynamic>)['stops'] as String,
        paymentMethods: List<String>.from(json['paymentMethods'] as List? ?? const []),
        states: ((json['lifecycle'] as Map<String, dynamic>)['states'] as List)
            .map((e) => StateInfo.fromJson(e as Map<String, dynamic>))
            .toList(),
        terms: Map<String, String>.from(json['terms'] as Map? ?? const {}),
      );

  bool has(String module) => modules.contains(module);

  /// Whether anyone can sign up (the brand offers the customer role).
  bool get openSignUp => roles.contains('customer');

  /// Minor units in the brand's currency.
  String money(num minor) => brand.currency.format(minor.toInt());

  /// The pack's word for a core noun: term('job') is "order" for food,
  /// "delivery" for courier. Falls back to the key itself, readable enough.
  String term(String key) => terms[key] ?? key;

  /// term, capitalised for a title or the start of a sentence.
  String title(String key) {
    final w = term(key);
    return w.isEmpty ? w : w[0].toUpperCase() + w.substring(1);
  }

  StateInfo? state(String name) {
    for (final s in states) {
      if (s.name == name) return s;
    }
    return null;
  }

  String stateLabel(String name) => state(name)?.label ?? name;

  String milestoneOf(String name) => state(name)?.milestone ?? '';

  /// The steps a job shows as progress: every state in lifecycle order,
  /// minus the cancelled one (a cancelled job shows that instead).
  List<StateInfo> get progress => states.where((s) => s.milestone != 'cancelled').toList();
}
