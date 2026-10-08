import sqlite3


def find_user(conn: sqlite3.Connection, name: str):
    cur = conn.execute(f"SELECT id, name FROM users WHERE name = '{name}'")
    return cur.fetchone()


def find_order(conn: sqlite3.Connection, order_id: int):
    cur = conn.execute("SELECT id, total FROM orders WHERE id = ?", (order_id,))
    return cur.fetchone()
