import 'dotenv/config';
import fs from 'node:fs';

const MOONDREAM_API_KEY = process.env.MOONDREAM_API_KEY;
const KERNEL_API_BASE = process.env.KERNEL_API_BASE || 'http://localhost:444';

if (!MOONDREAM_API_KEY) {
  console.error('Error: MOONDREAM_API_KEY not found in environment');
  process.exit(1);
}

const log = (msg) => console.log(`[MOONDREAM] ${new Date().toISOString()} ${msg}`);
const sleep = (ms) => new Promise(r => setTimeout(r, ms));

class KernelClient {
  constructor(baseUrl) {
    this.baseUrl = baseUrl.replace(/\/$/, '');
    this.width = 1920; // default fallback
    this.height = 1080; // default fallback
  }

  async init() {
    try {
    } catch (e) {
      log('Failed to get display config, using defaults');
    }
  }

  async takeScreenshot() {
    const res = await fetch(`${this.baseUrl}/computer/screenshot`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({}) 
    });
    if (!res.ok) throw new Error(`Screenshot failed: ${res.status}`);
    const buffer = await res.arrayBuffer();
    return Buffer.from(buffer);
  }

  async click(x, y) {
    log(`Clicking at ${x}, ${y}`);
    const res = await fetch(`${this.baseUrl}/computer/click_mouse`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ x: Math.round(x), y: Math.round(y), button: 'left', click_type: 'click' })
    });
    if (!res.ok) throw new Error(`Click failed: ${res.status}`);
  }

  async type(text) {
    log(`Typing: "${text}"`);
    const res = await fetch(`${this.baseUrl}/computer/type`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text, delay: 100 })
    });
    if (!res.ok) throw new Error(`Type failed: ${res.status}`);
  }
}

class MoondreamClient {
  constructor(apiKey) {
    this.apiKey = apiKey;
    this.baseUrl = 'https://api.moondream.ai/v1';
  }

  async query(imageBuffer, question, reasoning) {
    const base64Image = `data:image/jpeg;base64,${imageBuffer.toString('base64')}`;
    log(`Querying Moondream: "${question}"`);
    
    const res = await fetch(`${this.baseUrl}/query`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Moondream-Auth': this.apiKey
      },
      body: JSON.stringify({
        image_url: base64Image,
        question: question,
		reasoning: reasoning,
      })
    });

    if (!res.ok) {
        const txt = await res.text();
        throw new Error(`Moondream Query failed: ${res.status} ${txt}`);
    }

    const data = await res.json();
    log(`Answer: ${data.answer}`);
    return data.answer.toLowerCase().includes('true');
  }

  async point(imageBuffer, objectLabel) {
    const base64Image = `data:image/jpeg;base64,${imageBuffer.toString('base64')}`;
    log(`Pointing Moondream: "${objectLabel}"`);

    const res = await fetch(`${this.baseUrl}/point`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Moondream-Auth': this.apiKey
      },
      body: JSON.stringify({
        image_url: base64Image,
        object: objectLabel
      })
    });

    if (!res.ok) {
         const txt = await res.text();
         throw new Error(`Moondream Point failed: ${res.status} ${txt}`);
    }

    const data = await res.json();
    if (data.points && data.points.length > 0) {
      log(`Points found: ${JSON.stringify(data.points)}`);
      return data.points[0]; // {x: 0.5, y: 0.5}
    }
    log('No points found');
    return null;
  }
}

// Utility to read image dimensions from PNG header (simple parsing to avoid sharp/jimp dependency)
function getPngDimensions(buffer) {
    // PNG signature: 89 50 4E 47 0D 0A 1A 0A
    // IHDR chunk starts at byte 8
    // Width at byte 16 (4 bytes), Height at byte 20 (4 bytes)
    if (buffer.readUInt32BE(0) !== 0x89504E47) { // check signature start
         // Fallback or assume it's JPEG? Kernel API returns PNG by default.
         return { width: 1280, height: 720 };
    }
    const width = buffer.readUInt32BE(16);
    const height = buffer.readUInt32BE(20);
    return { width, height };
}

async function runMoondreamSequence() {
  const kernel = new KernelClient(KERNEL_API_BASE);
  const moon = new MoondreamClient(MOONDREAM_API_KEY);

  log('Starting MOONDREAM Agent...');

  const steps = [
    {
      type: 'query_click',
      query: "does this screenshot have a popup that asks to Enable Microphone ? answer with true|false",
      point: "Button to enable microphone"
    },
    {
      type: 'query_click',
      query: "does this screenshot have a popup that asks to Allow microphone access ? answer with true|false",
      point: "Button to \"Allow while visiting the site\""
    },
    {
      type: 'query_type_click',
      query: "does this screenshot have a visible useable text field to type Name into ? answer with true|false",
      point: "Text input field to type Name into",
      text: "KERNELAI"
    },
    {
      type: 'query_click',
      query: "does this screenshot have a visible enabled button to 'Join now' ? answer with true|false",
      point: "Button to \"Join now\""
    },
    {
      type: 'query_click',
      query: "do you see the bottom bar controls buttons with icons for : microphone , camera , emoji , hand , hangup ? answer with true|false",
      point: "laptop monitor square icon button in bottom bar", // lmao reverse engineer moondream first to get icon names as it sees it
	  // override for now because moondream struggles to find the proper icon
	  override_points: [
			{
				"x": 0.4789051808406647,
				"y": 0.9628543499511242,
			}
	  ],
    },
    {
      type: 'query_click',
	  pre_sleep_ms: 2_000,
      query: "do you see a panel that says that 'Others may see your video differently' ? answer with true|false",
      point: "Blue action Text 'Got it'",
	  reasoning: true,
    },
  ];

  for (const step of steps) {
	if (step.pre_sleep_ms) {
		await new Promise(resolve => setTimeout(resolve, step.pre_sleep_ms));
	}
    if (step.type === 'query_click' || step.type === 'query_type_click') {
      let attempts = 0;
      const maxAttempts = 6;
      let success = false;

      while (attempts < maxAttempts) {
        log(`Step: ${step.query} (Attempt ${attempts + 1}/${maxAttempts})`);
        
        const screenshot = await kernel.takeScreenshot();
        const dims = getPngDimensions(screenshot);
        
		const reasoning = step?.reasoning ? true : false
        const isTrue = await moon.query(screenshot, step.query, reasoning);
        
        if (isTrue) {
          log('Condition met.');
          // Point
		      
          // override case points[]
          const coords = step.override_points ? step.override_points[0] : await moon.point(screenshot, step.point);
          if (coords) {
             const pixelX = coords.x * dims.width;
             const pixelY = coords.y * dims.height;
             if (step.type === 'query_type_click') {
                 await kernel.click(pixelX, pixelY); // Focus
                 await sleep(750);
                 await kernel.type(step.text);
             } else {
                 await kernel.click(pixelX, pixelY);
             }
             success = true;
             break; // Next step
          } else {
              log('Condition true but point failed.');
          }
        } else {
          log('Condition false. Sleeping 1s...');
          await sleep(1000);
        }
        attempts++;
      }
      
      if (!success) {
          log(`Step failed or timed out after ${maxAttempts} attempts. Moving to next step anyway? Or aborting?`);
          // Proceeding might be safer if it was just a false negative, or maybe we assume it's already done.
      }
    } 
    else if (step.type === 'point_click') {
        log(`Step: Pointing "${step.point}"`);
        const screenshot = await kernel.takeScreenshot();
        const dims = getPngDimensions(screenshot);
        const coords = await moon.point(screenshot, step.point);
        if (coords) {
            const pixelX = coords.x * dims.width;
            const pixelY = coords.y * dims.height;
            await kernel.click(pixelX, pixelY);
        } else {
            log('Point failed.');
        }
    }
    
    // Small delay between steps
    await sleep(2000);
  }
  
  log('MOONDREAM sequence completed.');
}

runMoondreamSequence().catch(e => {
  console.error(e);
  process.exit(1);
});
