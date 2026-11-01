import 'dart:convert';

/// Returns a stable URL for long-lived records (history/favorites).
///
/// Gateway-signed image URLs (`/api/img/<token>`) embed an expiry. Persisting
/// one in the database means that after the token expires the cover can never
/// load again (the browser keeps requesting a URL the gateway now rejects).
/// The token payload still contains the original upstream URL, so unwrap it
/// and store that instead; the app signs again on demand.
String stableImageUrl(String url) {
  if (url.isEmpty) return url;
  String token;
  try {
    final path = Uri.parse(url).path;
    const prefix = '/api/img/';
    if (!path.startsWith(prefix)) return url;
    token = path.substring(prefix.length);
  } catch (_) {
    return url;
  }
  if (token.isEmpty || token.contains('/')) return url;
  try {
    final payload = token.split('.').first;
    final decoded = utf8.decode(base64Url.decode(base64Url.normalize(payload)));
    final data = jsonDecode(decoded);
    if (data is Map && data['url'] is String) {
      final raw = data['url'] as String;
      final uri = Uri.tryParse(raw);
      if (uri != null && (uri.scheme == 'http' || uri.scheme == 'https')) {
        return raw;
      }
    }
  } catch (_) {
    // Older/foreign token format: keep it rather than losing the cover.
  }
  return url;
}
