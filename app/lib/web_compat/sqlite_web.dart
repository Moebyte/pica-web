/// Web side of the SQLite facade.
///
/// `sqlite3.wasm` is loaded from the web root (copied from `web/sqlite3.wasm`)
/// and persists databases through `IndexedDbFileSystem`. If the wasm binary
/// cannot be loaded or the IndexedDB VFS is unavailable, an in-memory VFS is
/// used, and if even that fails every database becomes a no-op stub so that
/// the UI can still start (history/favorites/cookies are simply empty).
library;

import 'package:sqlite3/wasm.dart' as wasm;

wasm.WasmSqlite3? _sqlite;
bool _usingIndexedDb = false;

/// Whether the real wasm database is available.
bool get webDatabaseAvailable => _sqlite != null;

/// Whether databases are persisted in IndexedDB.
bool get webDatabasePersistent => _usingIndexedDb;

/// Loads the sqlite3 wasm binary and registers a virtual file system.
///
/// Must be awaited once before [openDb] is called.
Future<void> initWebDatabase() async {
  if (_sqlite != null) {
    return;
  }
  try {
    final sqlite =
        await wasm.WasmSqlite3.loadFromUrl(Uri.parse('sqlite3.wasm'));
    try {
      final fs = await wasm.IndexedDbFileSystem.open(dbName: 'pica_comic_web');
      sqlite.registerVirtualFileSystem(fs, makeDefault: true);
      _usingIndexedDb = true;
    } catch (_) {
      final fs = wasm.InMemoryFileSystem();
      sqlite.registerVirtualFileSystem(fs, makeDefault: true);
      _usingIndexedDb = false;
    }
    _sqlite = sqlite;
  } catch (_) {
    _sqlite = null;
    _usingIndexedDb = false;
  }
}

class Row {
  Row._(this._row);

  final wasm.Row? _row;

  /// Empty/zero row used by the no-op fallback database for count queries.
  Row._zero() : _row = null;

  dynamic operator [](Object? key) {
    final row = _row;
    if (row != null) {
      return row[key];
    }
    if (key is int) {
      return 0;
    }
    return 0;
  }

  List<Object?> get values {
    final row = _row;
    if (row != null) {
      return row.values;
    }
    return const [0];
  }

  @override
  String toString() => _row?.toString() ?? 'Row(0)';
}

class Database {
  Database._(this._db);

  /// A database that silently accepts writes and returns empty results.
  Database._null() : _db = null;

  final wasm.CommonDatabase? _db;

  List<Row> select(String sql, [List<Object?> parameters = const []]) {
    final db = _db;
    if (db == null) {
      final lower = sql.toLowerCase();
      if (lower.contains('count(')) {
        return [Row._zero()];
      }
      return const [];
    }
    return db
        .select(sql, parameters)
        .map((e) => Row._(e))
        .toList(growable: false);
  }

  void execute(String sql, [List<Object?> parameters = const []]) {
    _db?.execute(sql, parameters);
  }

  void dispose() {
    _db?.dispose();
  }
}

Database openDb(String path) {
  final sqlite = _sqlite;
  if (sqlite == null) {
    return Database._null();
  }
  try {
    return Database._(sqlite.open(path));
  } catch (_) {
    return Database._null();
  }
}
