# Blinkogram

**Blinkogram** is an easy to use integration service for syncing messages and images from a Telegram bot into your Blinko.

## Prerequisites

- Telegram Bot

## Installation

Download the binary files for your operating system from the [Releases](https://github.com/wolfsilver/blinko-telegram/releases) page.

## Configuration

Create a `.env` file in the project's root directory and add the following configuration:

```env
SERVER_ADDR=https://blinko.up.railway.app
BOT_TOKEN=your_telegram_bot_token
BOT_PROXY_ADDR=https://api.your_proxy_addr.com
```

The `SERVER_ADDR` should be your self hosted server address that the Blinko is running on.

## Usage

### Starting the Service

#### Starting with binary

1. Download and extract the released binary file;
2. Create a `.env` file in the same directory as the binary file;
3. Run the executable in the terminal:

   ```sh
   ./blinkogram
   ```

4. Once the bot is running, you can interact with it via your Telegram bot.

#### Starting with Docker

Or you can start the service with Docker:

1.  Build the Docker image: `docker build -t blinkogram .`
2.  Run the Docker container with the required environment variables:

    ```sh
    docker run -d --name blinkogram \
    -e SERVER_ADDR=https://blinko.up.railway.app \
    -e BOT_TOKEN=your_telegram_bot_token \
    blinkogram
    ```

3.  The Blinkogram service should now be running inside the Docker container. You can interact with it via your Telegram bot.

#### Starting with Docker Compose

Or you can start the service with Docker Compose. This can be combined with the `blinko` itself in the same compose file:

1.  Create a folder where the service will be located.
2.  Clone this repository in a subfolder `git clone https://github.com/wolfsilver/blinko-telegram blinkogram`
3.  Create `.env` file
    ```sh
    SERVER_ADDR=https://blinko.up.railway.app
    BOT_TOKEN=your_telegram_bot_token
    ```
4.  Create Docker Compose `docker-compose.yml` file:
    ```yaml
    services:
      blinkogram:
        env_file: .env
        build: blinkogram
        container_name: blinkogram
    ```
5.  Run the bot via `docker compose up -d`
6.  The Blinkogram service should now be running inside the Docker container. You can interact with it via your Telegram bot.

### Interaction Commands

- `/start <access_token>`: Start the bot with your Blinko access token.
- Send text messages: Save the message content as a memo.
- Send files (photos, documents): Save the files as resources in a memo.
- `/search <words>`: Search for the memos.

### References
> [memogram](https://github.com/usememos/memogram)