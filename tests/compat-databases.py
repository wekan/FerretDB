#!/usr/bin/env python3
"""The development services and the MySQL-compatible servers the mysql backend
is tested against.

`task env-up` runs `docker compose`, and docker-compose.yml has run WeKan
against FerretDB since it took that name: the development services moved to
docker-compose.dev.yml, and env-up asked docker-compose.yml for services it
does not have. The Taskfile now names docker-compose.dev.yml (COMPOSE_FILE),
every service it starts is in it, and MariaDB and TiDB - on their own ports -
agree between the compose file, `envtool setup` and their test tasks.
Negative: docker-compose.yml has none of them, and a missing service is caught.
"""

import os
import re
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def read(rel):
    with open(os.path.join(ROOT, rel), encoding="utf-8") as f:
        return f.read()


def services(compose):
    body = compose.split("\nservices:\n", 1)[1]
    body = re.split(r"\n(?=\S)", body, 1)[0]
    return set(re.findall(r"^  ([a-z_]+):\s*$", body, re.M))


def missing(taskfile, compose):
    wanted = re.search(r"^  SERVICES: (.+)$", taskfile, re.M).group(1).split()
    return [s for s in wanted if s not in services(compose)]


class CompatDatabases(unittest.TestCase):
    def setUp(self):
        self.task = read("Taskfile.yml")
        self.dev = read("docker-compose.dev.yml")
        self.envtool = read("cmd/envtool/envtool.go")

    def test_env_up_uses_the_development_compose_file(self):
        self.assertRegex(self.task, r"(?m)^env:\n(?:  .*\n)*?  COMPOSE_FILE: docker-compose\.dev\.yml$")
        self.assertEqual(missing(self.task, self.dev), [])

    def test_mariadb_and_tidb(self):
        for name, port, dockerfile, image in (("mariadb", 3307, "mariadb.Dockerfile", "mariadb:"),
                                              ("tidb", 4000, "tidb.Dockerfile", "pingcap/tidb:")):
            self.assertIn(f"  {name}:\n", self.dev)
            block = self.dev.split(f"\n  {name}:\n", 1)[1].split("\n\n", 1)[0]
            self.assertIn(f"dockerfile: {dockerfile}", block, name)
            self.assertRegex(block, rf"- {port}:\d+", name)
            self.assertTrue(read(f"build/deps/{dockerfile}").startswith("FROM " + image), name)
            task = self.task.split(f"\n  test-integration-{name}:\n", 1)[1].split("\n\n", 1)[0]
            self.assertIn("-target-backend=ferretdb-mysql", task, name)
            self.assertIn(f"-mysql-url='mysql://username:password@127.0.0.1:{port}/ferretdb'", task, name)
            self.assertIn(f"127.0.0.1:{port}/", self.envtool, name)
        setup = self.envtool.split("func setup(", 1)[1].split("\n}\n", 1)[0]
        for f in ("setupMySQL,", "setupMariaDB,", "setupTiDB,"):
            self.assertIn(f, setup)
        # TiDB's image runs no init scripts: envtool makes the user and the database.
        tidb = self.envtool.split("func setupTiDB(", 1)[1].split("\n}\n", 1)[0]
        self.assertIn("CREATE USER IF NOT EXISTS 'username'@'%' IDENTIFIED BY 'password'", tidb)
        self.assertIn("CREATE DATABASE IF NOT EXISTS ferretdb", tidb)

    def test_negative(self):
        # docker-compose.yml runs WeKan: none of the development services is there.
        self.assertTrue(missing(self.task, read("docker-compose.yml")))
        self.assertEqual(missing("  SERVICES: mysql nosuch\n", self.dev), ["nosuch"])


if __name__ == "__main__":
    unittest.main()
