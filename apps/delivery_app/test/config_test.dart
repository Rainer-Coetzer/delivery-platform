import 'package:delivery_app/core/config.dart';
import 'package:flutter_test/flutter_test.dart';

// The shape GET /config returns (pkg/packs.PublicConfig), for the courier pack.
final _courier = {
  'pack': {'name': 'courier', 'description': 'Parcels'},
  'locale': 'en',
  'modules': ['packages', 'capacity', 'proof', 'scheduling'],
  'roles': ['customer', 'agent', 'admin'],
  'job': {'stops': 'point_to_point', 'lines': 'packages'},
  'paymentMethods': ['card', 'cash'],
  'lifecycle': {
    'initial': 'BOOKED',
    'states': [
      {'name': 'BOOKED', 'label': 'Booked', 'milestone': 'created'},
      {'name': 'FINDING_COURIER', 'label': 'Finding a courier', 'milestone': 'ready_for_dispatch'},
      {'name': 'IN_TRANSIT', 'label': 'On the way', 'milestone': 'in_transit'},
      {'name': 'DELIVERED', 'label': 'Delivered', 'milestone': 'completed', 'terminal': true},
      {'name': 'CANCELLED', 'label': 'Cancelled', 'milestone': 'cancelled', 'terminal': true},
    ],
  },
  'terms': {'job': 'delivery', 'jobs': 'deliveries', 'agent': 'courier'},
  'limits': {},
};

void main() {
  final config = AppConfig.fromJson(_courier);

  test('words come from the pack, with a readable fallback', () {
    expect(config.term('job'), 'delivery');
    expect(config.title('agent'), 'Courier');
    expect(config.term('merchant'), 'merchant'); // courier doesn't name one
  });

  test('progress is the lifecycle minus the cancelled state', () {
    expect(config.progress.map((s) => s.name), ['BOOKED', 'FINDING_COURIER', 'IN_TRANSIT', 'DELIVERED']);
    expect(config.stateLabel('IN_TRANSIT'), 'On the way');
    expect(config.milestoneOf('CANCELLED'), 'cancelled');
    expect(config.stateLabel('UNKNOWN'), 'UNKNOWN');
  });

  test('modules and payment methods', () {
    expect(config.has('proof'), isTrue);
    expect(config.has('catalogue'), isFalse);
    expect(config.paymentMethods, ['card', 'cash']);
  });

  test('money is minor units', () {
    expect(money(4300), 'N\$43.00');
  });
}
