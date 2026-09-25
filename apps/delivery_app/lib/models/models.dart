// Plain, hand-written models with fromJson/toJson matching the JSON shapes
// each backend module returns (see each module's internal/api package).
// No codegen, so `flutter pub get` is the only step needed before running
// the app. Names are the platform's generic domain (ADR-0003); what a
// person reads comes from the pack (core/config.dart).

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

class Merchant {
  final String id;
  final String name;
  final double lat;
  final double lng;
  final bool isOpen;

  Merchant({required this.id, required this.name, required this.lat, required this.lng, required this.isOpen});

  factory Merchant.fromJson(Map<String, dynamic> json) => Merchant(
        id: json['id'] as String,
        name: json['name'] as String,
        lat: (json['lat'] as num).toDouble(),
        lng: (json['lng'] as num).toDouble(),
        isOpen: (json['isOpen'] as bool?) ?? true,
      );
}

class CatalogueItem {
  final String id;
  final String merchantId;
  final String name;
  final int priceCents;
  final int stockQty;
  final bool available;

  CatalogueItem({
    required this.id,
    required this.merchantId,
    required this.name,
    required this.priceCents,
    required this.stockQty,
    required this.available,
  });

  factory CatalogueItem.fromJson(Map<String, dynamic> json) => CatalogueItem(
        id: json['id'] as String,
        merchantId: json['merchantId'] as String,
        name: json['name'] as String,
        priceCents: (json['priceCents'] as num).toInt(),
        stockQty: (json['stockQty'] as num).toInt(),
        available: json['available'] as bool,
      );
}

class CartLine {
  final CatalogueItem item;
  int quantity;
  CartLine({required this.item, this.quantity = 1});
  int get subtotalCents => item.priceCents * quantity;
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

class Agent {
  final String id;
  final String name;
  final String vehicle;
  final String status;
  final double lat;
  final double lng;

  Agent({required this.id, required this.name, required this.vehicle, required this.status, required this.lat, required this.lng});

  factory Agent.fromJson(Map<String, dynamic> json) => Agent(
        id: json['id'] as String,
        name: json['name'] as String,
        vehicle: json['vehicle'] as String? ?? '',
        status: json['status'] as String,
        lat: (json['lat'] as num).toDouble(),
        lng: (json['lng'] as num).toDouble(),
      );
}

class NotificationDto {
  final String id;
  final String title;
  final String body;
  final String? jobId;

  NotificationDto({required this.id, required this.title, required this.body, this.jobId});

  factory NotificationDto.fromJson(Map<String, dynamic> json) => NotificationDto(
        id: json['id'] as String,
        title: json['title'] as String,
        body: json['body'] as String,
        jobId: json['jobId'] as String?,
      );
}
