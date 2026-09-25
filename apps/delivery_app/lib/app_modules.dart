import 'core/module.dart';
import 'extensions.dart';
import 'modules/admin/admin_module.dart';
import 'modules/agent/agent_module.dart';
import 'modules/auth/auth_module.dart';
import 'modules/catalogue/catalogue_module.dart';
import 'modules/jobs/jobs_module.dart';
import 'modules/merchant/merchant_module.dart';

/// Every module the app is built from, in the order homes are looked up:
/// catalogue before jobs, so a customer on a platform with a catalogue
/// starts by browsing it. The buyer's modules (lib/extensions.dart) come
/// last, so they add to ours rather than taking over our homes; a buyer
/// who wants their module to be a role's home says so in homeFor and
/// removes ours from this list at their own upgrade cost.
const List<AppModule> appModules = [
  AuthModule(),
  CatalogueModule(),
  JobsModule(),
  MerchantModule(),
  AgentModule(),
  AdminModule(),
  ...extensionModules,
];
