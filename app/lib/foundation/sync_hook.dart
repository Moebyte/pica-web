/// 基础层与各数据层的“账号态已变化”通知钩子。
///
/// 独立成叶子文件是为了避免 base.dart / comic_source.dart 与
/// GatewaySync 之间的循环 import；由 GatewaySync 在启动时注册实现。
void Function()? accountSyncHook;

/// 通知账号态发生变化（登录/登出/凭据更新），由 GatewaySync 去抖推送。
void scheduleAccountSync() {
  accountSyncHook?.call();
}
