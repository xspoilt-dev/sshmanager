#!/usr/bin/env bun
import { select, input, password, confirm, Separator } from "@inquirer/prompts";
import { existsSync, readFileSync, writeFileSync, mkdirSync } from "fs";
import { join } from "path";
import { spawnSync, spawn } from "child_process";
import { homedir } from "os";

// ─── Storage ────────────────────────────────────────────────────────────────

const CONFIG_DIR = join(homedir(), ".config", "sshmanager");
const DATA_FILE = join(CONFIG_DIR, "servers.json");

interface Server {
	alias: string;
	host: string;
	user: string;
	password: string;
	port: number;
}

function ensureDir(): void {
	mkdirSync(CONFIG_DIR, { recursive: true });
}

function loadServers(): Server[] {
	ensureDir();
	if (!existsSync(DATA_FILE)) return [];
	try {
		return JSON.parse(readFileSync(DATA_FILE, "utf-8")) as Server[];
	} catch {
		return [];
	}
}

function saveServers(servers: Server[]): void {
	ensureDir();
	// mode 0o600 — readable only by owner
	writeFileSync(DATA_FILE, JSON.stringify(servers, null, 2), { mode: 0o600 });
}

// ─── SSH ────────────────────────────────────────────────────────────────────

const IS_WINDOWS = process.platform === "win32";

function hasCommand(cmd: string): boolean {
	const finder = IS_WINDOWS ? "where" : "which";
	return spawnSync(finder, [cmd], { shell: IS_WINDOWS }).status === 0;
}

async function connect(server: Server): Promise<void> {
	let child;

	if (IS_WINDOWS) {
		// On Windows use plink (PuTTY) which supports -pw
		if (!hasCommand("plink")) {
			console.error(
				"\n  \x1b[31merror:\x1b[0m plink not found.\n" +
				"  Install PuTTY from https://www.putty.org/ and make sure plink.exe is in your PATH.\n",
			);
			process.exit(1);
		}

		console.log(
			`\n  \x1b[36mConnecting to\x1b[0m \x1b[1m${server.alias}\x1b[0m` +
				`  \x1b[2m${server.user}@${server.host}:${server.port}\x1b[0m\n`,
		);

		child = spawn(
			"plink",
			[
				"-ssh",
				"-pw", server.password,
				"-P", String(server.port),
				`${server.user}@${server.host}`,
			],
			{ stdio: "inherit", shell: false },
		);
	} else {
		// On Linux/macOS use sshpass
		if (!hasCommand("sshpass")) {
			console.error(
				"\n  \x1b[31merror:\x1b[0m sshpass not found — install it with: sudo apt install sshpass\n",
			);
			process.exit(1);
		}

		console.log(
			`\n  \x1b[36mConnecting to\x1b[0m \x1b[1m${server.alias}\x1b[0m` +
				`  \x1b[2m${server.user}@${server.host}:${server.port}\x1b[0m\n`,
		);

		child = spawn(
			"sshpass",
			[
				"-p", server.password,
				"ssh",
				"-p", String(server.port),
				"-o", "StrictHostKeyChecking=no",
				"-o", "ConnectTimeout=10",
				`${server.user}@${server.host}`,
			],
			{ stdio: "inherit" },
		);
	}

	await new Promise<void>((resolve) => {
		child.on("close", () => resolve());
	});
}

// ─── Helpers ────────────────────────────────────────────────────────────────

function parseUserAtHost(
	str: string,
): { user: string; host: string; port: number } | null {
	const match = str.match(/^([^@]+)@([^:]+)(?::(\d+))?$/);
	if (!match) return null;
	const port = match[3] ? Number(match[3]) : 22;
	if (!Number.isInteger(port) || port <= 0 || port >= 65536) return null;
	return { user: match[1], host: match[2], port };
}

// ─── Prompts ─────────────────────────────────────────────────────────────────

async function promptAdd(servers: Server[]): Promise<Server[]> {
	console.log();

	const alias = (
		await input({
			message: "Alias:",
			validate: (v) => {
				if (!v.trim()) return "Required";
				if (servers.some((s) => s.alias === v.trim()))
					return "Alias already exists";
				return true;
			},
		})
	).trim();

	const host = (
		await input({
			message: "Host / IP:",
			validate: (v) => (v.trim() ? true : "Required"),
		})
	).trim();

	const portStr = await input({
		message: "Port:",
		default: "22",
		validate: (v) => {
			const n = Number(v);
			return Number.isInteger(n) && n > 0 && n < 65536
				? true
				: "Invalid port";
		},
	});

	const user = (
		await input({
			message: "Username:",
			default: "root",
			validate: (v) => (v.trim() ? true : "Required"),
		})
	).trim();

	const pass = await password({
		message: "Password:",
		mask: "*",
		validate: (v) => (v ? true : "Required"),
	});

	const updated = [
		...servers,
		{ alias, host, user, password: pass, port: Number(portStr) },
	];
	saveServers(updated);
	console.log(`\n  \x1b[32mSaved "${alias}".\x1b[0m\n`);
	return updated;
}

async function promptRemove(servers: Server[]): Promise<Server[]> {
	if (servers.length === 0) {
		console.log("\n  No servers saved.\n");
		return servers;
	}

	console.log();
	const alias = await select<string | null>({
		message: "Remove which server?",
		choices: [
			...servers.map((s) => ({
				name: `${s.alias.padEnd(16)} \x1b[2m${s.user}@${s.host}:${s.port}\x1b[0m`,
				value: s.alias,
			})),
			new Separator(),
			{ name: "Cancel", value: null },
		],
	});

	if (!alias) return servers;

	const confirmed = await confirm({
		message: `Remove "${alias}"?`,
		default: false,
	});
	if (!confirmed) return servers;

	const updated = servers.filter((s) => s.alias !== alias);
	saveServers(updated);
	console.log(`\n  \x1b[33mRemoved "${alias}".\x1b[0m\n`);
	return updated;
}

// ─── TUI ────────────────────────────────────────────────────────────────────

type MenuAction =
	| { type: "connect"; alias: string }
	| { type: "add" | "remove" | "exit" };

async function runTUI(): Promise<void> {
	console.log("\n  \x1b[1m\x1b[36mSSH Manager\x1b[0m\n");

	let servers = loadServers();

	while (true) {
		const choices: any[] = [];

		if (servers.length > 0) {
			for (const s of servers) {
				choices.push({
					name: `  ${s.alias.padEnd(16)} \x1b[2m${s.user}@${s.host}:${s.port}\x1b[0m`,
					value: { type: "connect", alias: s.alias } as MenuAction,
				});
			}
		} else {
			choices.push({
				name: "  \x1b[2m(no servers — add one below)\x1b[0m",
				value: null,
				disabled: true,
			});
		}

		choices.push(new Separator());
		choices.push({
			name: "  Add server",
			value: { type: "add" } as MenuAction,
		});
		choices.push({
			name: "  Remove server",
			value: { type: "remove" } as MenuAction,
		});
		choices.push({ name: "  Exit", value: { type: "exit" } as MenuAction });

		const action = await select<MenuAction | null>({
			message: "Servers",
			choices,
			pageSize: 20,
		});

		if (!action || action.type === "exit") break;

		if (action.type === "connect") {
			const server = servers.find(
				(s) => s.alias === (action as any).alias,
			)!;
			await connect(server);
		} else if (action.type === "add") {
			servers = await promptAdd(servers);
		} else if (action.type === "remove") {
			servers = await promptRemove(servers);
		}
	}

	console.log();
}

// ─── Entry point ─────────────────────────────────────────────────────────────

async function main(): Promise<void> {
	const [, , ...args] = process.argv;

	// CLI mode: sshmanager <alias>  or  sshmanager user@host[:port]
	if (args.length > 0) {
		const arg = args[0];
		const servers = loadServers();

		// Try as alias first
		const byAlias = servers.find(
			(s) => s.alias.toLowerCase() === arg.toLowerCase(),
		);
		if (byAlias) {
			await connect(byAlias);
			return;
		}

		// Try as user@host[:port]
		const parsed = parseUserAtHost(arg);
		if (parsed) {
			const existing = servers.find(
				(s) =>
					s.user === parsed.user &&
					s.host === parsed.host &&
					s.port === parsed.port,
			);
			if (existing) {
				await connect(existing);
				return;
			}

			// Unknown entry — ask for alias and password, save, then connect
			console.log(`\n  \x1b[33mNo saved entry for "${arg}". Adding it now.\x1b[0m\n`);

			const alias = (
				await input({
					message: "Alias:",
					validate: (v) => {
						if (!v.trim()) return "Required";
						if (servers.some((s) => s.alias === v.trim()))
							return "Alias already exists";
						return true;
					},
				})
			).trim();

			const pass = await password({
				message: "Password:",
				mask: "*",
				validate: (v) => (v ? true : "Required"),
			});

			const newServer: Server = {
				alias,
				host: parsed.host,
				user: parsed.user,
				password: pass,
				port: parsed.port,
			};
			saveServers([...servers, newServer]);
			console.log(`\n  \x1b[32mSaved "${alias}".\x1b[0m\n`);
			await connect(newServer);
			return;
		}

		// Unrecognised argument
		console.error(`\n  \x1b[31mNo server with alias "${arg}"\x1b[0m`);
		if (servers.length > 0) {
			console.log(
				`  Available: ${servers.map((s) => s.alias).join(", ")}`,
			);
		}
		console.log();
		process.exit(1);
	}

	// Interactive TUI mode
	await runTUI();
}

main().catch((err) => {
	// Ctrl+C inside an inquirer prompt
	if (err?.name === "ExitPromptError") {
		console.log();
		process.exit(0);
	}
	console.error(err);
	process.exit(1);
});
