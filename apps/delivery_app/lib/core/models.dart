// The models every module shares: a point, the logged-in user, and the
// job (the core's noun, ADR-0003). A module's own models live in the
// module (modules/catalogue/models.dart). Hand-written fromJson, no
// codegen, so `flutter pub get` is the only step before running the app.
// What a person reads comes from the pack (core/config.dart).

class LatLng2 {
  final double lat;
  final double lng;
  const LatLng2(this.lat, this.lng);

  factory LatLng2.fromJson(Map<String, dynamic> json) =>
      LatLng2((json['lat'] as num).toDouble(), (json['lng'] as num).toDouble());

  Map<String, dynamic> toJson() => {'lat': lat, 'lng': lng};
}

class User {
  final String id;
  final String name;
  final String email;
  final String role;
  final String refId;

  User({required this.id, required this.name, required this.email, required this.role, required this.refId});

  factory User.fromJson(Map<String, dynamic> json) => User(
        id: json['id'] as String,
        name: json['name'] as String,
        email: json['email'] as String,
        role: json['role'] as String,
        refId: (json['refId'] as String?) ?? '',
      );
}

class JobLine {
  final String kind; // catalogue or package
  final String catalogueItemId;
  final String name;
  final int quantity;
  final int unitPriceCents;

  JobLine({required this.kind, required this.catalogueItemId, required this.name, required this.quantity, required this.unitPriceCents});

  factory JobLine.fromJson(Map<String, dynamic> json) => JobLine(
        kind: json['kind'] as String? ?? 'catalogue',
        catalogueItemId: json['catalogueItemId'] as String? ?? '',
        name: json['name'] as String,
        quantity: (json['quantity'] as num).toInt(),
        unitPriceCents: (json['unitPriceCents'] as num?)?.toInt() ?? 0,
      );
}

class Job {
  final String id;
  final String customerId;
  final String merchantId;
  final String merchantName;
  final String status; // the pack's state name
  final String milestone; // what code decides on
  final List<JobLine> items;
  final int subtotalCents;
  final int deliveryFeeCents;
  final double surgeMultiplier;
  final int totalCents;
  final String agentId;
  final List<String> actions; // states this caller may move the job to

  Job({
    required this.id,
    required this.customerId,
    required this.merchantId,
    required this.merchantName,
    required this.status,
    required this.milestone,
    required this.items,
    required this.subtotalCents,
    required this.deliveryFeeCents,
    required this.surgeMultiplier,
    required this.totalCents,
    required this.agentId,
    required this.actions,
  });

  bool get finished => milestone == 'completed' || milestone == 'cancelled';

  factory Job.fromJson(Map<String, dynamic> json) => Job(
        id: json['id'] as String,
        customerId: json['customerId'] as String? ?? '',
        merchantId: json['merchantId'] as String? ?? '',
        merchantName: json['merchantName'] as String? ?? '',
        status: json['status'] as String,
        milestone: json['milestone'] as String? ?? '',
        items: ((json['items']) as List<dynamic>? ?? []).map((e) => JobLine.fromJson(e as Map<String, dynamic>)).toList(),
        subtotalCents: (json['subtotalCents'] as num?)?.toInt() ?? 0,
        deliveryFeeCents: (json['deliveryFeeCents'] as num?)?.toInt() ?? 0,
        surgeMultiplier: (json['surgeMultiplier'] as num?)?.toDouble() ?? 1.0,
        totalCents: (json['totalCents'] as num?)?.toInt() ?? 0,
        agentId: json['agentId'] as String? ?? '',
        actions: List<String>.from(json['actions'] as List? ?? const []),
      );
}
