// The catalogue module's models: merchants, their catalogue items, and the
// customer's cart lines.

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
  final int quantity;
  const CartLine({required this.item, this.quantity = 1});
  int get subtotalCents => item.priceCents * quantity;
  CartLine withQuantity(int q) => CartLine(item: item, quantity: q);
}
