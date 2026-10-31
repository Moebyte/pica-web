/// Native (io) side of the SQLite facade.
///
/// This keeps the original `package:sqlite3` FFI implementation and merely
/// wraps its rows so that the rest of the app is independent of the backend.
library;

import 'package:sqlite3/sqlite3.dart' as sqlite;

/// A query result row. Indexable by column name (`row["name"]`) or index
/// (`row[0]`).
class Row {
  Row._(this._row);

  final sqlite.Row _row;

  dynamic operator [](Object? key) => _row[key];

  List<Object?> get values => _row.values;

  @override
  String toString() => _row.toString();
}

/// Synchronous database handle.
class Database {
  Database._(this._db);

  final sqlite.Database _db;

  List<Row> select(String sql, [List<Object?> parameters = const []]) {
    return _db
        .select(sql, parameters)
        .map((e) => Row._(e))
        .toList(growable: false);
  }

  void execute(String sql, [List<Object?> parameters = const []]) {
    _db.execute(sql, parameters);
  }

  void dispose() {
    _db.dispose();
  }
}

Database openDb(String path) {
  return Database._(sqlite.sqlite3.open(path));
}

/// No-op on native platforms.
Future<void> initWebDatabase() async {}
