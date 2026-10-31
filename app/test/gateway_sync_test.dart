import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:pica_comic/network/gateway_sync.dart';

void main() {
  test('mergeSourceData lets server fields win and follows logout', () {
    final stored = <String, dynamic>{
      'account': ['old-user', 'old-pass'],
      'imageQuality': 'original',
    };
    final incoming = <String, dynamic>{
      'account': ['new-user', 'new-pass'],
      'appChannel': '3',
    };
    final merged = GatewaySync.mergeSourceData(stored, incoming);
    expect(merged['account'], ['new-user', 'new-pass']);
    expect(merged['imageQuality'], 'original');
    expect(merged['appChannel'], '3');
    expect(stored['account'], ['old-user', 'old-pass']); // 不改入参

    // 对端登出（account=null）本地跟随
    final loggedOut = GatewaySync.mergeSourceData(merged, {'account': null});
    expect(loggedOut.containsKey('account'), isTrue);
    expect(loggedOut['account'], isNull);
  });

  test('blob survives a json round trip', () {
    final blob = {
      'v': 1,
      'implicitData': ['a', 'b'],
      'settings': {'85': 'www.cdnhjk.net'},
      'sources': {
        'jm': {'account': ['u', 'p']}
      },
      'cookies': [
        {'name': 'AVS', 'value': 'x', 'domain': 'www.cdnhjk.net', 'path': '/'}
      ],
    };
    final decoded = jsonDecode(jsonEncode(blob));
    expect(decoded, isA<Map<String, dynamic>>());
    expect((decoded['sources'] as Map)['jm'], isA<Map>());
  });
}
