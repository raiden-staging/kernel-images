/**
 * Detects the current screen resolution from the X display
 * and updates the environment variables accordingly
 * might be useful later
 */

import { execCapture } from './src/utils/exec.js'


async function detectScreenResolution() {
  try {
    // Get the current DISPLAY value
    const display = process.env.DISPLAY || ':0'
    console.log(`Detecting screen resolution for display: ${display}`)
    
    // Run xrandr to get screen information
    const { stdout } = await execCapture('xrandr', ['--current'])
    const output = stdout.toString()
    
    // Parse the output to find the current resolution
    // Looking for lines like "1920x1080" or "1280x720+0+0"
    const resolutionMatch = output.match(/current (\d+) x (\d+)/) || 
                            output.match(/(\d+)x(\d+)\+\d+\+\d+/)
    
    if (resolutionMatch) {
      const width = resolutionMatch[1]
      const height = resolutionMatch[2]
      
      console.log(`Detected screen resolution: ${width}x${height}`)
      
      // Override environment variables
      process.env.SCREEN_WIDTH = width
      process.env.SCREEN_HEIGHT = height
      
      console.log(`Updated environment variables: SCREEN_WIDTH=${width}, SCREEN_HEIGHT=${height}`)
    } else {
      console.warn('Could not detect screen resolution, using default values')
    }
  } catch (error) {
    console.error('Error detecting screen resolution:', error.message)
    console.log('Using default screen resolution values')
  }
}

// Run the detection function
detectScreenResolution().catch(err => {
  console.error('Failed to detect screen resolution:', err)
})
