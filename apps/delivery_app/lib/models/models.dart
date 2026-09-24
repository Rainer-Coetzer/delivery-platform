// Plain, hand-written models with fromJson/toJson matching the JSON shapes
// each backend service returns (see each service's internal/api package).
// No codegen, so `flutter pub get` is the only step needed before running the app.

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

class Restaurant {
  final String id;
  final String name;
  final double lat;
  final double lng;
  final bool isOpen;

  Restaurant({required this.id, required this.name, required this.lat, required this.lng, required this.isOpen});

  factory Restaurant.fromJson(Map<String, dynamic> json) => Restaurant(
        id: json['id'] as String,
        name: json['name'] as String,
        lat: (json['lat'] as num).toDouble(),
        lng: (json['lng'] as num).toDouble(),
        isOpen: (json['isOpen'] as bool?) ?? true,
      );
}

class MenuItem {
  final String id;
  final String restaurantId;
  final String name;
  final int priceCents;
  final int stockQty;
  final bool available;

  MenuItem({
    required this.id,
    required this.restaurantId,
    required this.name,
    required this.priceCents,
    required this.stockQty,
    required this.available,
  });

  factory MenuItem.fromJson(Map<String, dynamic> json) => MenuItem(
        id: json['id'] as String,
        restaurantId: json['restaurantId'] as String,
        name: json['name'] as String,
        priceCents: (json['priceCents'] as num).toInt(),
        stockQty: (json['stockQty'] as num).toInt(),
        available: json['available'] as bool,
      );
}

class CartLine {
  final MenuItem item;
  int quantity;
  CartLine({required this.item, this.quantity = 1});
  int get subtotalCents => item.priceCents * quantity;
}

class OrderItemDto {
  final String menuItemId;
  final String name;
  final int quantity;
  final int unitPriceCents;

  OrderItemDto({required this.menuItemId, required this.name, required this.quantity, required this.unitPriceCents});

  Map<String, dynamic> toJson() => {
        'menuItemId': menuItemId,
        'name': name,
        'quantity': quantity,
        'unitPriceCents': unitPriceCents,
      };

  factory OrderItemDto.fromJson(Map<String, dynamic> json) => OrderItemDto(
        menuItemId: json['menuItemId'] as String,
        name: json['name'] as String,
        quantity: (json['quantity'] as num).toInt(),
        unitPriceCents: (json['unitPriceCents'] as num).toInt(),
      );
}

class Order {
  final String id;
  final String customerId;
  final String restaurantId;
  final String restaurantName;
  final String status;
  final List<OrderItemDto> items;
  final int subtotalCents;
  final int deliveryFeeCents;
  final double surgeMultiplier;
  final int totalCents;
  final String driverId;

  Order({
    required this.id,
    required this.customerId,
    required this.restaurantId,
    required this.restaurantName,
    required this.status,
    required this.items,
    required this.subtotalCents,
    required this.deliveryFeeCents,
    required this.surgeMultiplier,
    required this.totalCents,
    required this.driverId,
  });

  factory Order.fromJson(Map<String, dynamic> json) => Order(
        id: json['id'] as String,
        customerId: json['customerId'] as String? ?? '',
        restaurantId: json['restaurantId'] as String? ?? '',
        restaurantName: json['restaurantName'] as String? ?? '',
        status: json['status'] as String,
        items: ((json['items']) as List<dynamic>? ?? [])
            .map((e) => OrderItemDto.fromJson(e as Map<String, dynamic>))
            .toList(),
        subtotalCents: (json['subtotalCents'] as num?)?.toInt() ?? 0,
        deliveryFeeCents: (json['deliveryFeeCents'] as num?)?.toInt() ?? 0,
        surgeMultiplier: (json['surgeMultiplier'] as num?)?.toDouble() ?? 1.0,
        totalCents: (json['totalCents'] as num?)?.toInt() ?? 0,
        driverId: json['driverId'] as String? ?? '',
      );
}

class Driver {
  final String id;
  final String name;
  final String vehicle;
  final String status;
  final double lat;
  final double lng;

  Driver({required this.id, required this.name, required this.vehicle, required this.status, required this.lat, required this.lng});

  factory Driver.fromJson(Map<String, dynamic> json) => Driver(
        id: json['id'] as String,
        name: json['name'] as String,
        vehicle: json['vehicle'] as String? ?? 'bike',
        status: json['status'] as String,
        lat: (json['lat'] as num).toDouble(),
        lng: (json['lng'] as num).toDouble(),
      );
}

class NotificationDto {
  final String id;
  final String title;
  final String body;
  final String? orderId;

  NotificationDto({required this.id, required this.title, required this.body, this.orderId});

  factory NotificationDto.fromJson(Map<String, dynamic> json) => NotificationDto(
        id: json['id'] as String,
        title: json['title'] as String,
        body: json['body'] as String,
        orderId: json['orderId'] as String?,
      );
}
